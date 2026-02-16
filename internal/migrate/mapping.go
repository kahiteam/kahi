package migrate

import (
	"fmt"
	"strconv"
	"strings"
)

// OptionMapping describes how to convert a supervisord option to Kahi TOML.
type OptionMapping struct {
	KahiKey     string // target key in Kahi TOML (empty = same name)
	Type        string // "string", "int", "bool", "stringlist", "intlist", "bytes"
	Unsupported bool   // true if not supported in Kahi
	Renamed     string // original supervisord name if renamed
}

// programMappings maps supervisord [program:x] options to Kahi equivalents.
var programMappings = map[string]OptionMapping{
	"command":                 {Type: "string"},
	"process_name":            {Type: "string"},
	"numprocs":                {Type: "int"},
	"numprocs_start":          {Type: "int"},
	"priority":                {Type: "int"},
	"autostart":               {Type: "bool"},
	"autorestart":             {Type: "string"},
	"startsecs":               {Type: "int"},
	"startretries":            {Type: "int"},
	"exitcodes":               {Type: "intlist"},
	"stopsignal":              {Type: "signal"},
	"stopwaitsecs":            {Type: "int"},
	"stopasgroup":             {Type: "bool"},
	"killasgroup":             {Type: "bool"},
	"user":                    {Type: "string"},
	"directory":               {Type: "string"},
	"umask":                   {Type: "string"},
	"environment":             {Type: "env"},
	"redirect_stderr":         {Type: "bool"},
	"stdout_logfile":          {Type: "string"},
	"stdout_logfile_maxbytes": {Type: "bytes"},
	"stdout_logfile_backups":  {Type: "int"},
	"stdout_capture_maxbytes": {Type: "bytes"},
	"stdout_syslog":           {Type: "bool"},
	"stderr_logfile":          {Type: "string"},
	"stderr_logfile_maxbytes": {Type: "bytes"},
	"stderr_logfile_backups":  {Type: "int"},
	"stderr_capture_maxbytes": {Type: "bytes"},
	"stderr_syslog":           {Type: "bool"},
	"stdout_events_enabled":   {Unsupported: true},
	"stderr_events_enabled":   {Unsupported: true},
	"serverurl":               {Unsupported: true},
}

// supervisordMappings maps [supervisord] section options.
var supervisordMappings = map[string]OptionMapping{
	"logfile":          {KahiKey: "logfile", Type: "string"},
	"logfile_maxbytes": {Unsupported: true},
	"logfile_backups":  {Unsupported: true},
	"loglevel":         {KahiKey: "log_level", Type: "string", Renamed: "loglevel"},
	"pidfile":          {Unsupported: true},
	"nodaemon":         {Unsupported: true},
	"silent":           {Unsupported: true},
	"minfds":           {Type: "int"},
	"minprocs":         {Type: "int"},
	"nocleanup":        {Type: "bool"},
	"childlogdir":      {Unsupported: true},
	"user":             {Unsupported: true},
	"directory":        {KahiKey: "directory", Type: "string"},
	"strip_ansi":       {Unsupported: true},
	"environment":      {Unsupported: true},
	"identifier":       {Type: "string"},
}

// groupMappings maps [group:x] section options.
var groupMappings = map[string]OptionMapping{
	"programs": {Type: "stringlist"},
	"priority": {Type: "int"},
}

// MappedOption holds a converted key-value pair ready for TOML output.
type MappedOption struct {
	Key         string
	Value       string // TOML-formatted value
	Comment     string // optional inline comment
	Unsupported bool
}

// MapProgramOption converts a single supervisord program option to Kahi TOML.
func MapProgramOption(key, value string) MappedOption {
	return mapOption(key, value, programMappings)
}

// MapSupervisordOption converts a single supervisord daemon option to Kahi TOML.
func MapSupervisordOption(key, value string) MappedOption {
	return mapOption(key, value, supervisordMappings)
}

// MapGroupOption converts a single supervisord group option to Kahi TOML.
func MapGroupOption(key, value string) MappedOption {
	return mapOption(key, value, groupMappings)
}

func mapOption(key, value string, mappings map[string]OptionMapping) MappedOption {
	mapping, ok := mappings[key]
	if !ok {
		return MappedOption{
			Key:         key,
			Value:       fmt.Sprintf("%q", value),
			Comment:     "UNSUPPORTED: unknown option",
			Unsupported: true,
		}
	}

	if mapping.Unsupported {
		return MappedOption{
			Key:         key,
			Value:       value,
			Comment:     fmt.Sprintf("UNSUPPORTED: %s = %s", key, value),
			Unsupported: true,
		}
	}

	kahiKey := mapping.KahiKey
	if kahiKey == "" {
		kahiKey = key
	}

	var comment string
	if mapping.Renamed != "" {
		comment = fmt.Sprintf("renamed from %q", mapping.Renamed)
	}

	tomlValue := convertValue(value, mapping.Type)

	return MappedOption{
		Key:     kahiKey,
		Value:   tomlValue,
		Comment: comment,
	}
}

// convertValue converts a supervisord value to TOML syntax.
func convertValue(value, typ string) string {
	switch typ {
	case "int":
		if v, err := strconv.Atoi(value); err == nil {
			return strconv.Itoa(v)
		}
		return fmt.Sprintf("%q", value)

	case "bool":
		b, err := ParseBool(value)
		if err != nil {
			return fmt.Sprintf("%q", value)
		}
		return strconv.FormatBool(b)

	case "string":
		return fmt.Sprintf("%q", value)

	case "signal":
		return fmt.Sprintf("%q", NormalizeSignal(value))

	case "bytes":
		// Preserve human-readable format.
		return fmt.Sprintf("%q", value)

	case "intlist":
		// e.g. "0,2" -> [0, 2]
		parts := strings.Split(value, ",")
		var nums []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if v, err := strconv.Atoi(p); err == nil {
				nums = append(nums, strconv.Itoa(v))
			}
		}
		return "[" + strings.Join(nums, ", ") + "]"

	case "stringlist":
		// e.g. "web,api" -> ["web", "api"]
		parts := strings.Split(value, ",")
		var quoted []string
		for _, p := range parts {
			quoted = append(quoted, fmt.Sprintf("%q", strings.TrimSpace(p)))
		}
		return "[" + strings.Join(quoted, ", ") + "]"

	case "env":
		// supervisord format: KEY="val",KEY2="val2"
		return convertEnvironment(value)

	default:
		return fmt.Sprintf("%q", value)
	}
}

// NormalizeSignal normalizes a signal name to uppercase without SIG prefix.
func NormalizeSignal(sig string) string {
	sig = strings.TrimSpace(strings.ToUpper(sig))
	sig = strings.TrimPrefix(sig, "SIG")
	return sig
}

// convertEnvironment converts supervisord environment format to TOML table.
// Input: KEY="val",KEY2="val2"
// Output is handled specially by the caller.
func convertEnvironment(value string) string {
	return fmt.Sprintf("%q", value)
}
