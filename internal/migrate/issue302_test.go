package migrate

import (
	"strings"
	"testing"
)

// In a value Kahi does not expand, %(ENV_X)s gets X's value from this
// environment with a warning, or stays as it is when X is unset; it never
// becomes a literal ${X}. Values Kahi expands still get ${X} (#302).
func TestMigrateEnvInUnexpandedField(t *testing.T) {
	t.Setenv("ZZ_MIG_USER", "kahi")
	in := "[supervisord]\nuser=%(ENV_ZZ_MIG_USER)s\numask=%(ENV_ZZ_MIG_UNSET)s\n\n[program:a]\ncommand=/bin/a %(ENV_ZZ_MIG_USER)s\n"
	r, err := MigrateReader(strings.NewReader(in), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`user = "kahi"`, `command = "/bin/a ${ZZ_MIG_USER}"`} {
		if !strings.Contains(r.TOML, want) {
			t.Errorf("output lacks %s:\n%s", want, r.TOML)
		}
	}
	if strings.Contains(r.TOML, "${ZZ_MIG_UNSET}") || strings.Contains(r.TOML, `user = "${ZZ_MIG_USER}"`) {
		t.Errorf("a literal ${X} in a field Kahi does not expand:\n%s", r.TOML)
	}
	w := strings.Join(r.Warnings, "\n")
	for _, want := range []string{
		"supervisord.user: %(ENV_ZZ_MIG_USER)s replaced with the value of ZZ_MIG_USER in this environment (Kahi does not expand user)",
		"supervisord.umask: %(ENV_ZZ_MIG_UNSET)s left as is: ZZ_MIG_UNSET is not set here and Kahi does not expand umask",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings lack %q:\n%s", want, w)
		}
	}
	if strings.Contains(w, "program:a.command") {
		t.Errorf("an expanded field was substituted:\n%s", w)
	}
}
