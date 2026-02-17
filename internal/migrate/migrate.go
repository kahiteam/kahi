// Package migrate converts supervisord.conf INI files to Kahi TOML format.
package migrate

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/kahiteam/kahi/internal/config"
)

// Result holds the output of a migration run.
type Result struct {
	TOML      string   // generated TOML content
	Warnings  []string // non-fatal warnings (unsupported options, etc.)
	ParseErrs []string // errors from INI parsing
	ValidErrs []string // validation errors from generated config
}

// Options configures migration behavior.
type Options struct {
	Output string // write to file instead of stdout (empty = stdout)
	Force  bool   // overwrite existing output file
	DryRun bool   // preview only, no file write
}

// Migrate reads a supervisord.conf and produces a Kahi TOML config.
func Migrate(inputPath string, opts Options) (*Result, error) {
	f, err := os.Open(inputPath)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", inputPath)
	}
	defer f.Close()

	return MigrateReader(f, opts)
}

// MigrateReader converts a supervisord.conf from a reader to Kahi TOML.
func MigrateReader(r io.Reader, opts Options) (*Result, error) {
	ini, err := ParseINI(r)
	if err != nil {
		return &Result{ParseErrs: []string{err.Error()}}, err
	}

	result := &Result{
		Warnings: ini.Warnings,
	}

	toml := generateTOML(ini, result)
	result.TOML = toml

	// Validate the generated TOML.
	validateGenerated(result)

	return result, nil
}

// WriteResult writes migration output to the configured destination.
func WriteResult(result *Result, opts Options, w io.Writer) error {
	if opts.Output != "" && !opts.DryRun {
		if !opts.Force {
			if _, err := os.Stat(opts.Output); err == nil {
				return fmt.Errorf("output file exists: %s (use --force)", opts.Output)
			}
		}
		if err := os.WriteFile(opts.Output, []byte(result.TOML), 0644); err != nil {
			return fmt.Errorf("cannot write output: %w", err)
		}
		return nil
	}

	_, err := fmt.Fprint(w, result.TOML)
	return err
}

func generateTOML(ini *INIFile, result *Result) string {
	var b strings.Builder
	b.WriteString("# Kahi configuration file\n")
	b.WriteString("# Migrated from supervisord.conf\n\n")

	var supervisordSections []INISection
	var programSections []INISection
	var groupSections []INISection
	var includeSections []INISection
	var other []INISection

	for _, sec := range ini.Sections {
		switch sec.Type {
		case "supervisord":
			supervisordSections = append(supervisordSections, sec)
		case "program":
			programSections = append(programSections, sec)
		case "group":
			groupSections = append(groupSections, sec)
		case "include":
			includeSections = append(includeSections, sec)
		case "unix_http_server":
			writeUnixServerSection(&b, sec, result)
		case "inet_http_server":
			writeHTTPServerSection(&b, sec, result)
		default:
			other = append(other, sec)
		}
	}

	for _, sec := range supervisordSections {
		writeSupervisorSection(&b, sec)
	}

	sort.Slice(programSections, func(i, j int) bool {
		return programSections[i].Name < programSections[j].Name
	})
	for _, sec := range programSections {
		writeProgramSection(&b, sec, result)
	}

	sort.Slice(groupSections, func(i, j int) bool {
		return groupSections[i].Name < groupSections[j].Name
	})
	for _, sec := range groupSections {
		writeGroupSection(&b, sec)
	}

	for _, sec := range includeSections {
		writeIncludeSection(&b, sec)
	}

	for _, sec := range other {
		fmt.Fprintf(&b, "# UNSUPPORTED SECTION: [%s", sec.Type)
		if sec.Name != "" {
			b.WriteString(":" + sec.Name)
		}
		b.WriteString("]\n")
		for k, v := range sec.Options {
			fmt.Fprintf(&b, "# %s = %s\n", k, v)
		}
		b.WriteString("\n")
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("unsupported section type: %s", sec.Type))
	}

	return b.String()
}

func writeSupervisorSection(b *strings.Builder, sec INISection) {
	b.WriteString("[supervisor]\n")
	keys := sortedKeys(sec.Options)
	for _, key := range keys {
		mapped := MapSupervisordOption(key, sec.Options[key])
		writeOption(b, mapped)
	}
	b.WriteString("\n")
}

func writeProgramSection(b *strings.Builder, sec INISection, result *Result) {
	fmt.Fprintf(b, "[programs.%s]\n", sec.Name)

	var envPairs map[string]string
	keys := sortedKeys(sec.Options)

	for _, key := range keys {
		value := sec.Options[key]
		if key == "environment" {
			envPairs = parseEnvironment(value)
			continue
		}
		mapped := MapProgramOption(key, value)
		writeOption(b, mapped)
		if mapped.Unsupported {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("programs.%s: unsupported option %q", sec.Name, key))
		}
	}

	if len(envPairs) > 0 {
		fmt.Fprintf(b, "[programs.%s.environment]\n", sec.Name)
		envKeys := sortedKeys(envPairs)
		for _, k := range envKeys {
			fmt.Fprintf(b, "%s = %q\n", k, envPairs[k])
		}
	}
	b.WriteString("\n")
}

func writeGroupSection(b *strings.Builder, sec INISection) {
	fmt.Fprintf(b, "[groups.%s]\n", sec.Name)
	keys := sortedKeys(sec.Options)
	for _, key := range keys {
		mapped := MapGroupOption(key, sec.Options[key])
		writeOption(b, mapped)
	}
	b.WriteString("\n")
}

func writeUnixServerSection(b *strings.Builder, sec INISection, result *Result) {
	b.WriteString("[server.unix]\n")
	if file, ok := sec.Options["file"]; ok {
		fmt.Fprintf(b, "file = %q\n", file)
	}
	if chmod, ok := sec.Options["chmod"]; ok {
		fmt.Fprintf(b, "chmod = %q\n", chmod)
	}
	if chown, ok := sec.Options["chown"]; ok {
		fmt.Fprintf(b, "chown = %q\n", chown)
	}
	for k, v := range sec.Options {
		if k != "file" && k != "chmod" && k != "chown" {
			fmt.Fprintf(b, "# UNSUPPORTED: %s = %s\n", k, v)
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("server.unix: unsupported option %q", k))
		}
	}
	b.WriteString("\n")
}

func writeHTTPServerSection(b *strings.Builder, sec INISection, result *Result) {
	b.WriteString("[server.http]\n")
	b.WriteString("enabled = true\n")
	if port, ok := sec.Options["port"]; ok {
		fmt.Fprintf(b, "listen = %q\n", port)
	}
	if user, ok := sec.Options["username"]; ok {
		fmt.Fprintf(b, "username = %q\n", user)
	}
	if pass, ok := sec.Options["password"]; ok {
		fmt.Fprintf(b, "password = %q\n", pass)
	}
	for k, v := range sec.Options {
		if k != "port" && k != "username" && k != "password" {
			fmt.Fprintf(b, "# UNSUPPORTED: %s = %s\n", k, v)
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("server.http: unsupported option %q", k))
		}
	}
	b.WriteString("\n")
}

func writeIncludeSection(b *strings.Builder, sec INISection) {
	if files, ok := sec.Options["files"]; ok {
		fmt.Fprintf(b, "include = [%q]\n", files)
		b.WriteString("# NOTE: include files should be expanded and inlined for production use\n")
	}
	b.WriteString("\n")
}

func writeOption(b *strings.Builder, opt MappedOption) {
	if opt.Unsupported {
		b.WriteString("# " + opt.Comment + "\n")
		return
	}
	if opt.Comment != "" {
		fmt.Fprintf(b, "%s = %s # %s\n", opt.Key, opt.Value, opt.Comment)
	} else {
		fmt.Fprintf(b, "%s = %s\n", opt.Key, opt.Value)
	}
}

func parseEnvironment(value string) map[string]string {
	result := make(map[string]string)
	for pair := range strings.SplitSeq(value, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, "\"'")
		result[k] = v
	}
	return result
}

func validateGenerated(result *Result) {
	if result.TOML == "" {
		return
	}
	_, _, err := config.LoadBytes([]byte(result.TOML), "generated")
	if err != nil {
		result.ValidErrs = append(result.ValidErrs,
			fmt.Sprintf("generated config has validation errors: %s", err.Error()))
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
