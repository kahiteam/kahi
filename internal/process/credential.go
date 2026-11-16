package process

import (
	"syscall"

	"github.com/kahiteam/kahi/internal/userspec"
)

// resolveUser resolves a per-process user setting (ADR-046). Tests replace
// it so they never read the host's user database.
var resolveUser = userspec.Resolve

// ParseCredential resolves a user setting ("name", "uid", with an optional
// ":group") to the credential a child runs with: uid, primary gid and the
// user's supplementary groups (FUNC-046, SEC-010). An empty setting returns
// nil.
func ParseCredential(user string) (*syscall.Credential, error) {
	if user == "" {
		return nil, nil
	}
	c, err := resolveUser(user)
	if err != nil {
		return nil, err
	}
	return toSyscall(c), nil
}

// toSyscall converts a resolved credential. Groups is always set, so the
// child never keeps the supervisor's supplementary groups.
func toSyscall(c userspec.Credential) *syscall.Credential {
	groups := c.Groups
	if groups == nil {
		groups = []uint32{c.Gid}
	}
	return &syscall.Credential{Uid: c.Uid, Gid: c.Gid, Groups: groups}
}

// BuildSysProcAttr creates SysProcAttr with process group isolation
// and optional credential switching.
func BuildSysProcAttr(user string) (*syscall.SysProcAttr, error) {
	attr := &syscall.SysProcAttr{
		Setpgid: true,
	}

	cred, err := ParseCredential(user)
	if err != nil {
		return nil, err
	}
	if cred != nil {
		attr.Credential = cred
	}

	return attr, nil
}
