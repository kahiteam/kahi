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
	defer func() { _ = f.Close() }()

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

	// Includes first: in TOML a top-level key after a table header would
	// belong to that table.
	for _, sec := range ini.Sections {
		if sec.Type == "include" {
			writeIncludeSection(&b, sec, result)
		}
	}

	var supervisordSections []INISection
	var programSections []INISection
	var groupSections []INISection
	var listenerSections []INISection
	var other []INISection

	for _, sec := range ini.Sections {
		switch sec.Type {
		case "supervisord":
			supervisordSections = append(supervisordSections, sec)
		case "program":
			programSections = append(programSections, sec)
		case "group":
			groupSections = append(groupSections, sec)
		case "eventlistener":
			listenerSections = append(listenerSections, sec)
		case "include":
			// Written before the first table, above.
		case "unix_http_server":
			writeUnixServerSection(&b, sec, result)
		case "inet_http_server":
			writeHTTPServerSection(&b, sec, result)
		default:
			other = append(other, sec)
		}
	}

	for _, sec := range supervisordSections {
		writeSupervisorSection(&b, sec, result)
	}

	sort.Slice(programSections, func(i, j int) bool {
		return programSections[i].Name < programSections[j].Name
	})
	for _, sec := range programSections {
		writeProgramSection(&b, sec, result)
	}

	sort.Slice(listenerSections, func(i, j int) bool {
		return listenerSections[i].Name < listenerSections[j].Name
	})
	for _, sec := range listenerSections {
		writeListenerSection(&b, sec, result)
	}

	sort.Slice(groupSections, func(i, j int) bool {
		return groupSections[i].Name < groupSections[j].Name
	})
	for _, sec := range groupSections {
		writeGroupSection(&b, sec, result)
	}

	for _, sec := range other {
		writeComments(&b, sec.Above)
		fmt.Fprintf(&b, "# UNSUPPORTED SECTION: [%s", sec.Type)
		if sec.Name != "" {
			b.WriteString(":" + sec.Name)
		}
		b.WriteString("]")
		if sec.Comment != "" {
			b.WriteString(" # " + oneLine(sec.Comment))
		}
		b.WriteString("\n")
		for _, k := range sortedKeys(sec.Options) {
			writeUnsupported(&b, sec.Notes[k], fmt.Sprintf("%s = %s", k, sec.Options[k]))
		}
		writeComments(&b, sec.Trailing)
		b.WriteString("\n")
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("unsupported section type: %s", sec.Type))
	}

	return b.String()
}

func writeSupervisorSection(b *strings.Builder, sec INISection, result *Result) {
	writeHeader(b, "[supervisor]", sec)
	keys := sortedKeys(sec.Options)
	for _, key := range keys {
		mapped := MapSupervisordOption(key, sec.Options[key])
		writeOption(b, mapped, sec.Notes[key])
		if mapped.Unsupported {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("supervisord: unsupported option %q", key))
		}
	}
	writeComments(b, sec.Trailing)
	b.WriteString("\n")
}

func writeProgramSection(b *strings.Builder, sec INISection, result *Result) {
	writeProgramTable(b, sec, result, "programs", nil)
}

// writeProgramTable writes a program-like section as [table.name]; listener
// handles the keys only an event listener has and reports whether it did.
func writeProgramTable(b *strings.Builder, sec INISection, result *Result, table string, listener func(key, value string) bool) {
	writeHeader(b, fmt.Sprintf("[%s.%s]", table, tomlKey(sec.Name)), sec)

	var envPairs map[string]string
	keys := sortedKeys(sec.Options)

	for _, key := range keys {
		value := sec.Options[key]
		if key == "environment" {
			envPairs = parseEnvironment(value)
			continue
		}
		if listener != nil && listener(key, value) {
			continue
		}
		// supervisord's NONE (no log file) and AUTO (a file it names) have
		// no Kahi value: Kahi writes a log file only to a path it is given.
		if key == "stdout_logfile" || key == "stderr_logfile" {
			switch strings.ToUpper(strings.TrimSpace(value)) {
			case "NONE":
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("%s.%s: %s = NONE omitted (Kahi writes no log file unless a path is set)", table, sec.Name, key))
				continue
			case "AUTO":
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("%s.%s: %s = AUTO omitted (Kahi does not name log files; set a path to keep one)", table, sec.Name, key))
				continue
			}
		}
		// supervisord's stopwaitsecs = 0 SIGKILLs right after the stop
		// signal; Kahi rejects it (#138), so write the shortest valid value.
		if key == "stopwaitsecs" && strings.TrimSpace(value) == "0" {
			value = "1"
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%s.%s: stopwaitsecs = 0 is not allowed; wrote 1 (use stopsignal = SIGKILL for an immediate kill)", table, sec.Name))
		}
		mapped := MapProgramOption(key, value)
		writeOption(b, mapped, sec.Notes[key])
		if mapped.Unsupported {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%s.%s: unsupported option %q", table, sec.Name, key))
		}
	}

	// The section's end-of-section comments follow its last key, before
	// the environment subtable, so they stay in [programs.x].
	writeComments(b, sec.Trailing)
	if len(envPairs) > 0 {
		// Comments on environment= go above the subtable (FUNC-069).
		env := sec.Notes["environment"]
		writeComments(b, env.Above)
		if env.Inline != "" {
			writeComments(b, []string{env.Inline})
		}
		fmt.Fprintf(b, "[%s.%s.environment]\n", table, tomlKey(sec.Name))
		envKeys := sortedKeys(envPairs)
		for _, k := range envKeys {
			fmt.Fprintf(b, "%s = %s\n", tomlKey(k), tomlString(envPairs[k]))
		}
	}
	b.WriteString("\n")
}

func writeGroupSection(b *strings.Builder, sec INISection, result *Result) {
	writeHeader(b, fmt.Sprintf("[groups.%s]", tomlKey(sec.Name)), sec)
	keys := sortedKeys(sec.Options)
	for _, key := range keys {
		mapped := MapGroupOption(key, sec.Options[key])
		writeOption(b, mapped, sec.Notes[key])
		if mapped.Unsupported {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("groups.%s: unsupported option %q", sec.Name, key))
		}
	}
	writeComments(b, sec.Trailing)
	b.WriteString("\n")
}

func writeUnixServerSection(b *strings.Builder, sec INISection, result *Result) {
	writeHeader(b, "[server.unix]", sec)
	for _, k := range []string{"chmod", "file"} {
		if v, ok := sec.Options[k]; ok {
			writeOption(b, MappedOption{Key: k, Value: tomlString(v)}, sec.Notes[k])
		}
	}
	// chown is not emitted: the control socket is locked to the service
	// identity (owner-only) and Kahi rejects server.unix.chown, so it is
	// reported as unsupported rather than written as active config.
	for _, k := range sortedKeys(sec.Options) {
		if k != "file" && k != "chmod" {
			writeUnsupported(b, sec.Notes[k], fmt.Sprintf("UNSUPPORTED: %s = %s", k, sec.Options[k]))
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("server.unix: unsupported option %q", k))
		}
	}
	writeComments(b, sec.Trailing)
	b.WriteString("\n")
}

func writeHTTPServerSection(b *strings.Builder, sec INISection, result *Result) {
	writeHeader(b, "[server.http]", sec)
	b.WriteString("enabled = true\n")
	if port, ok := sec.Options["port"]; ok {
		writeOption(b, MappedOption{Key: "listen", Value: tomlString(listenAddress(port))}, sec.Notes["port"])
	}
	for _, k := range []string{"password", "username"} {
		if v, ok := sec.Options[k]; ok {
			writeOption(b, MappedOption{Key: k, Value: tomlString(v)}, sec.Notes[k])
		}
	}
	for _, k := range sortedKeys(sec.Options) {
		if k != "port" && k != "username" && k != "password" {
			writeUnsupported(b, sec.Notes[k], fmt.Sprintf("UNSUPPORTED: %s = %s", k, sec.Options[k]))
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("server.http: unsupported option %q", k))
		}
	}
	writeComments(b, sec.Trailing)
	b.WriteString("\n")
}

// listenAddress converts supervisord's inet_http_server port to a host:port
// listen address. supervisord reads "9001" and "*:9001" as all interfaces,
// which is ":9001" for Kahi.
func listenAddress(port string) string {
	if p, ok := strings.CutPrefix(port, "*:"); ok {
		return ":" + p
	}
	if !strings.Contains(port, ":") {
		return ":" + port
	}
	return port
}

// writeIncludeSection writes supervisord's [include] as a commented-out
// include. supervisord includes INI files and kahi includes TOML, so the
// included files must be migrated one by one before the include can be used.
func writeIncludeSection(b *strings.Builder, sec INISection, result *Result) {
	files, ok := sec.Options["files"]
	if !ok {
		return
	}
	globs := strings.Fields(files)
	quoted := make([]string, len(globs))
	for i, g := range globs {
		quoted[i] = tomlString(g)
	}
	writeComments(b, sec.Above)
	writeComments(b, sec.Notes["files"].Above)
	b.WriteString("# supervisord includes INI files; migrate each one with `kahi migrate`,\n")
	b.WriteString("# then enable this include with the .toml names:\n")
	fmt.Fprintf(b, "# include = [%s]\n\n", strings.Join(quoted, ", "))
	result.Warnings = append(result.Warnings,
		fmt.Sprintf("[include] files = %s: included INI files are not converted; migrate each and enable the commented include", files))
}

// writeOption writes one key with its comments (FUNC-069): those above it
// first; the user's inline comment at the end of the line, then a note such
// as renamed from "x" in parentheses; for a key written as # UNSUPPORTED,
// its inline comment goes above as "# (from supervisord) ...".
func writeOption(b *strings.Builder, opt MappedOption, c OptionComments) {
	if opt.Unsupported {
		writeUnsupported(b, c, opt.Comment)
		return
	}
	writeComments(b, c.Above)
	trail := oneLine(c.Inline)
	switch {
	case trail != "" && opt.Comment != "":
		trail += " (" + opt.Comment + ")"
	case opt.Comment != "":
		trail = opt.Comment
	}
	if trail != "" {
		fmt.Fprintf(b, "%s = %s # %s\n", opt.Key, opt.Value, trail)
	} else {
		fmt.Fprintf(b, "%s = %s\n", opt.Key, opt.Value)
	}
}

// writeUnsupported writes a commented-out line with the key's comments.
func writeUnsupported(b *strings.Builder, c OptionComments, line string) {
	writeComments(b, c.Above)
	if c.Inline != "" {
		b.WriteString("# (from supervisord) " + oneLine(c.Inline) + "\n")
	}
	b.WriteString("# " + line + "\n")
}

// writeHeader writes a table header with the comments above it and its
// inline comment at the end of the line.
func writeHeader(b *strings.Builder, title string, sec INISection) {
	writeComments(b, sec.Above)
	if sec.Comment != "" {
		fmt.Fprintf(b, "%s # %s\n", title, oneLine(sec.Comment))
		return
	}
	b.WriteString(title + "\n")
}

// writeComments writes each comment as one "# " line.
func writeComments(b *strings.Builder, lines []string) {
	for _, l := range lines {
		if t := oneLine(l); t != "" {
			b.WriteString("# " + t + "\n")
		} else {
			b.WriteString("#\n")
		}
	}
}

// oneLine collapses whitespace, so a comment is one TOML comment line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func parseEnvironment(value string) map[string]string {
	return splitEnvironment(value)
}

func validateGenerated(result *Result) {
	if result.TOML == "" {
		return
	}
	// The generated file is the whole config (includes stay commented), so
	// it gets the daemon's merged-config checks too: migrate must not accept
	// a config the daemon then refuses (#245).
	cfg, _, err := config.LoadBytes([]byte(result.TOML), "generated")
	if err == nil {
		if errs := config.ValidateMerged(cfg); len(errs) > 0 {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			err = fmt.Errorf("config validation failed in generated:\n  %s", strings.Join(msgs, "\n  "))
		}
	}
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
