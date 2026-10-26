package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandContext holds variables available for expansion.
type ExpandContext struct {
	Here        string // directory of the config file
	ProgramName string
	ProcessNum  int
	GroupName   string
	NumProcs    int
	// KeepProcessNum leaves %(process_num)d in place, for programs with
	// numprocs > 1: each instance substitutes its own number later
	// (process.ExpandNumprocs).
	KeepProcessNum bool
}

// ExpandVariables expands template variables and environment references
// in all string fields of a config, given the config file path.
func ExpandVariables(cfg *Config, configPath string) error {
	ctx := ExpandContext{
		Here: filepath.Dir(configPath),
	}

	// Expand supervisor fields.
	var err error
	cfg.Supervisor.Logfile, err = expandString(cfg.Supervisor.Logfile, ctx)
	if err != nil {
		return fmt.Errorf("supervisor.logfile: %w", err)
	}
	cfg.Supervisor.Directory, err = expandString(cfg.Supervisor.Directory, ctx)
	if err != nil {
		return fmt.Errorf("supervisor.directory: %w", err)
	}

	// Expand server fields.
	cfg.Server.Unix.File, err = expandString(cfg.Server.Unix.File, ctx)
	if err != nil {
		return fmt.Errorf("server.unix.file: %w", err)
	}

	// Expand program fields.
	for name, p := range cfg.Programs {
		pCtx := ctx
		pCtx.ProgramName = name
		pCtx.GroupName = name
		pCtx.NumProcs = p.Numprocs
		pCtx.ProcessNum = p.NumprocsStart
		pCtx.KeepProcessNum = p.Numprocs > 1

		p.Command, err = expandString(p.Command, pCtx)
		if err != nil {
			return fmt.Errorf("programs.%s.command: %w", name, err)
		}
		p.Directory, err = expandString(p.Directory, pCtx)
		if err != nil {
			return fmt.Errorf("programs.%s.directory: %w", name, err)
		}
		p.StdoutLogfile, err = expandString(p.StdoutLogfile, pCtx)
		if err != nil {
			return fmt.Errorf("programs.%s.stdout_logfile: %w", name, err)
		}
		p.StderrLogfile, err = expandString(p.StderrLogfile, pCtx)
		if err != nil {
			return fmt.Errorf("programs.%s.stderr_logfile: %w", name, err)
		}
		p.ProcessName, err = expandString(p.ProcessName, pCtx)
		if err != nil {
			return fmt.Errorf("programs.%s.process_name: %w", name, err)
		}
		p.User, err = expandString(p.User, pCtx)
		if err != nil {
			return fmt.Errorf("programs.%s.user: %w", name, err)
		}

		// Expand environment values.
		for k, v := range p.Environment {
			expanded, err := expandString(v, pCtx)
			if err != nil {
				return fmt.Errorf("programs.%s.environment.%s: %w", name, k, err)
			}
			p.Environment[k] = expanded
		}

		cfg.Programs[name] = p
	}

	return nil
}

// expandString expands all template variables and env references in a single string.
func expandString(s string, ctx ExpandContext) (string, error) {
	if s == "" {
		return s, nil
	}

	// Phase 1: Expand %(variable)s and %(variable)d patterns.
	result, err := expandTemplateVars(s, ctx)
	if err != nil {
		return "", err
	}

	// Phase 2: Expand ${ENV_VAR} references.
	result, err = expandEnvVars(result)
	if err != nil {
		return "", err
	}

	// Phase 3: Unescape %% -> % and $$ -> $.
	result = strings.ReplaceAll(result, "%%", "%")
	result = strings.ReplaceAll(result, "$$", "$")

	return result, nil
}

func expandTemplateVars(s string, ctx ExpandContext) (string, error) {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '%' && s[i+1] == '%' {
			// Escaped percent, preserve for later unescaping.
			result.WriteString("%%")
			i += 2
			continue
		}

		if i+1 < len(s) && s[i] == '%' && s[i+1] == '(' {
			// Find closing )s or )d.
			end := strings.Index(s[i:], ")s")
			endD := strings.Index(s[i:], ")d")
			if end < 0 && endD < 0 {
				return "", fmt.Errorf("unclosed template variable at position %d in %q", i, s)
			}

			var varName string
			var advance int
			if end >= 0 && (endD < 0 || end < endD) {
				varName = s[i+2 : i+end]
				advance = end + 2
			} else {
				varName = s[i+2 : i+endD]
				advance = endD + 2
			}

			val, err := resolveTemplateVar(varName, ctx)
			if err != nil {
				return "", err
			}
			result.WriteString(val)
			i += advance
			continue
		}

		result.WriteByte(s[i])
		i++
	}

	return result.String(), nil
}

func resolveTemplateVar(name string, ctx ExpandContext) (string, error) {
	switch name {
	case "here":
		return ctx.Here, nil
	case "program_name":
		return ctx.ProgramName, nil
	case "process_num":
		if ctx.KeepProcessNum {
			return "%(process_num)d", nil
		}
		return fmt.Sprintf("%d", ctx.ProcessNum), nil
	case "group_name":
		return ctx.GroupName, nil
	case "numprocs":
		return fmt.Sprintf("%d", ctx.NumProcs), nil
	default:
		return "", fmt.Errorf("unknown template variable: %%(%s)s", name)
	}
}

func expandEnvVars(s string) (string, error) {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '$' {
			// Escaped dollar, preserve for later unescaping.
			result.WriteString("$$")
			i += 2
			continue
		}

		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			end := strings.Index(s[i:], "}")
			if end < 0 {
				return "", fmt.Errorf("unclosed environment variable reference at position %d in %q", i, s)
			}

			varName := s[i+2 : i+end]
			val, ok := os.LookupEnv(varName)
			if !ok {
				return "", fmt.Errorf("undefined environment variable: ${%s}", varName)
			}
			result.WriteString(val)
			i += end + 1
			continue
		}

		result.WriteByte(s[i])
		i++
	}

	return result.String(), nil
}

// ExpandString is exported for use by other packages needing single-value expansion.
func ExpandString(s string, ctx ExpandContext) (string, error) {
	return expandString(s, ctx)
}
