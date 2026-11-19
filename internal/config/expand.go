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

	// Expand program fields; event listener pools are programs too.
	for name, p := range cfg.Programs {
		if err := expandProgram(&p, "programs", name, ctx); err != nil {
			return err
		}
		cfg.Programs[name] = p
	}
	for name, l := range cfg.EventListeners {
		if err := expandProgram(&l.ProgramConfig, "eventlisteners", name, ctx); err != nil {
			return err
		}
		cfg.EventListeners[name] = l
	}

	return expandWebhooks(cfg, ctx)
}

// expandString expands template variables and environment references in a
// single left-to-right pass (FUNC-012): %(var)s / %(var)d and ${VAR} are
// replaced, %% and $$ are the escapes for a literal % and $, and a value put
// in by a substitution is never scanned again, so a $$ or %% inside an
// environment variable's value stays as it is (no recursion).
//
// With KeepProcessNum (numprocs > 1) %(process_num)d is left for each
// instance (ExpandProcessNum), so %% stays escaped and every % a
// substitution puts in is escaped as %% for that second pass.
func expandString(s string, ctx ExpandContext) (string, error) {
	if s == "" {
		return s, nil
	}
	insert := func(b *strings.Builder, v string) {
		if ctx.KeepProcessNum {
			v = strings.ReplaceAll(v, "%", "%%")
		}
		b.WriteString(v)
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "%%"):
			if ctx.KeepProcessNum {
				b.WriteString("%%")
			} else {
				b.WriteByte('%')
			}
			i += 2
		case strings.HasPrefix(s[i:], "$$"):
			b.WriteByte('$')
			i += 2
		case strings.HasPrefix(s[i:], "%("):
			end := strings.Index(s[i:], ")s")
			endD := strings.Index(s[i:], ")d")
			if end < 0 && endD < 0 {
				return "", fmt.Errorf("unclosed template variable at position %d", i)
			}
			if end < 0 || (endD >= 0 && endD < end) {
				end = endD
			}
			name := s[i+2 : i+end]
			if name == "process_num" && ctx.KeepProcessNum {
				b.WriteString("%(process_num)d")
			} else {
				val, err := resolveTemplateVar(name, ctx)
				if err != nil {
					return "", err
				}
				insert(&b, val)
			}
			i += end + 2
		case strings.HasPrefix(s[i:], "${"):
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return "", fmt.Errorf("unclosed environment variable reference at position %d", i)
			}
			name := s[i+2 : i+end]
			val, ok := os.LookupEnv(name)
			if !ok {
				return "", fmt.Errorf("undefined environment variable: ${%s}", name)
			}
			insert(&b, val)
			i += end + 1
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String(), nil
}

// ExpandProcessNum is the per-instance pass for numprocs > 1: it puts num
// in for %(process_num)d and turns the %% the load pass kept back into %.
func ExpandProcessNum(s string, num int) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "%%"):
			b.WriteByte('%')
			i += 2
		case strings.HasPrefix(s[i:], "%(process_num)d"):
			fmt.Fprintf(&b, "%d", num)
			i += len("%(process_num)d")
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

func resolveTemplateVar(name string, ctx ExpandContext) (string, error) {
	switch name {
	case "here":
		return ctx.Here, nil
	case "program_name":
		return ctx.ProgramName, nil
	case "process_num":
		return fmt.Sprintf("%d", ctx.ProcessNum), nil
	case "group_name":
		return ctx.GroupName, nil
	case "numprocs":
		return fmt.Sprintf("%d", ctx.NumProcs), nil
	default:
		return "", fmt.Errorf("unknown template variable: %%(%s)s", name)
	}
}

// ExpandString is exported for use by other packages needing single-value expansion.
func ExpandString(s string, ctx ExpandContext) (string, error) {
	return expandString(s, ctx)
}

// expandProgram expands a program's string fields; table and name locate it
// in error messages.
func expandProgram(p *ProgramConfig, table, name string, ctx ExpandContext) error {
	var err error
	pCtx := ctx
	pCtx.ProgramName = name
	pCtx.GroupName = name
	pCtx.NumProcs = p.Numprocs
	pCtx.ProcessNum = p.NumprocsStart
	pCtx.KeepProcessNum = p.Numprocs > 1

	p.Command, err = expandString(p.Command, pCtx)
	if err != nil {
		return fmt.Errorf("%s.%s.command: %w", table, name, err)
	}
	p.Directory, err = expandString(p.Directory, pCtx)
	if err != nil {
		return fmt.Errorf("%s.%s.directory: %w", table, name, err)
	}
	p.StdoutLogfile, err = expandString(p.StdoutLogfile, pCtx)
	if err != nil {
		return fmt.Errorf("%s.%s.stdout_logfile: %w", table, name, err)
	}
	p.StderrLogfile, err = expandString(p.StderrLogfile, pCtx)
	if err != nil {
		return fmt.Errorf("%s.%s.stderr_logfile: %w", table, name, err)
	}
	p.ProcessName, err = expandString(p.ProcessName, pCtx)
	if err != nil {
		return fmt.Errorf("%s.%s.process_name: %w", table, name, err)
	}
	p.User, err = expandString(p.User, pCtx)
	if err != nil {
		return fmt.Errorf("%s.%s.user: %w", table, name, err)
	}

	// Expand environment values.
	for k, v := range p.Environment {
		expanded, err := expandString(v, pCtx)
		if err != nil {
			return fmt.Errorf("%s.%s.environment.%s: %w", table, name, k, err)
		}
		p.Environment[k] = expanded
	}
	return nil
}
