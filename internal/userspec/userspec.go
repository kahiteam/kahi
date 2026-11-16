// Package userspec resolves a "user[:group]" setting to the uid, primary gid
// and supplementary groups a process runs with (FUNC-045, FUNC-046, SEC-010,
// ADR-046). It is a leaf package so config, process and supervisor can all
// use the same resolver.
//
// Lookups go through os/user. With CGO_ENABLED=0 on Linux that reads
// /etc/passwd and /etc/group only (no NSS, LDAP or SSSD); on macOS it uses
// the system library.
package userspec

import (
	"errors"
	"fmt"
	"io/fs"
	"os/user"
	"slices"
	"strconv"
	"strings"
)

// Credential is a resolved user setting.
type Credential struct {
	Uid    uint32
	Gid    uint32
	Groups []uint32 // supplementary groups, sorted, without duplicates

	Name     string // passwd user name; empty without an entry
	Home     string // passwd home directory; empty without an entry
	HasEntry bool   // the uid has a passwd entry

	// ExplicitGroup is set when the setting names a group (user:group).
	ExplicitGroup bool
}

// Matches reports whether a process running as uid and gid already has this
// identity: the uid is the same and, when the setting names a group, so is
// the gid.
func (c Credential) Matches(uid, gid int) bool {
	if int64(c.Uid) != int64(uid) {
		return false
	}
	return !c.ExplicitGroup || int64(c.Gid) == int64(gid)
}

// The user database, replaceable in tests so they never read the host's.
var (
	lookupUser   = user.Lookup
	lookupUserID = user.LookupId
	lookupGroup  = user.LookupGroup
	groupIDs     = func(u *user.User) ([]string, error) { return u.GroupIds() }
)

// Resolve parses spec ("name", "uid", "name:group", "name:gid", "uid:gid" or
// "uid:group") and looks it up. A numeric user is a uid even when a user of
// that name exists; a uid without a passwd entry gets gid = uid (or the given
// group) and that gid alone as its groups. With an entry the groups are the
// user's own list as initgroups(3) computes it, plus a given group.
func Resolve(spec string) (Credential, error) {
	name, group, hasGroup := strings.Cut(spec, ":")
	if name == "" || (hasGroup && group == "") {
		return Credential{}, fmt.Errorf("invalid user %q", spec)
	}

	var c Credential
	var entry *user.User
	if isNumeric(name) {
		uid, err := strconv.ParseUint(name, 10, 32)
		if err != nil {
			return Credential{}, fmt.Errorf("invalid user %q", spec)
		}
		c.Uid = uint32(uid)
		c.Gid = c.Uid
		// Any lookup failure means no entry: a numeric uid worked before
		// names existed, in images without /etc/passwd too.
		if u, err := lookupUserID(name); err == nil {
			entry = u
		}
	} else {
		u, err := lookupUser(name)
		if err != nil {
			if notFound(err) {
				return Credential{}, fmt.Errorf("user not found: %s", name)
			}
			return Credential{}, fmt.Errorf("cannot look up user %s: %w", name, err)
		}
		entry = u
	}

	if entry != nil {
		uid, err := strconv.ParseUint(entry.Uid, 10, 32)
		if err != nil {
			return Credential{}, fmt.Errorf("user %s has invalid uid %q", entry.Username, entry.Uid)
		}
		gid, err := strconv.ParseUint(entry.Gid, 10, 32)
		if err != nil {
			return Credential{}, fmt.Errorf("user %s has invalid gid %q", entry.Username, entry.Gid)
		}
		c.Uid, c.Gid = uint32(uid), uint32(gid)
		c.Name, c.Home, c.HasEntry = entry.Username, entry.HomeDir, true
	}

	if hasGroup {
		gid, err := resolveGroup(group, spec)
		if err != nil {
			return Credential{}, err
		}
		c.Gid = gid
		c.ExplicitGroup = true
	}

	groups := []uint32{c.Gid}
	if entry != nil {
		ids, err := groupIDs(entry)
		switch {
		case err == nil:
			for _, id := range ids {
				g, err := strconv.ParseUint(id, 10, 32)
				if err != nil {
					return Credential{}, fmt.Errorf("cannot list groups of user %s: invalid gid %q", entry.Username, id)
				}
				groups = append(groups, uint32(g))
			}
		case errors.Is(err, fs.ErrNotExist):
			// No group file: the passwd primary gid alone.
			groups = append(groups, uint32(mustGid(entry.Gid)))
		default:
			return Credential{}, fmt.Errorf("cannot list groups of user %s: %w", entry.Username, err)
		}
	}
	slices.Sort(groups)
	c.Groups = slices.Compact(groups)
	return c, nil
}

func resolveGroup(group, spec string) (uint32, error) {
	if isNumeric(group) {
		gid, err := strconv.ParseUint(group, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("invalid user %q", spec)
		}
		return uint32(gid), nil
	}
	g, err := lookupGroup(group)
	if err != nil {
		if notFound(err) {
			return 0, fmt.Errorf("group not found: %s", group)
		}
		return 0, fmt.Errorf("cannot look up group %s: %w", group, err)
	}
	gid, err := strconv.ParseUint(g.Gid, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("group %s has invalid gid %q", group, g.Gid)
	}
	return uint32(gid), nil
}

// mustGid parses a gid already validated by Resolve.
func mustGid(s string) uint64 {
	g, _ := strconv.ParseUint(s, 10, 32)
	return g
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// notFound reports a lookup that found no entry, including a missing
// /etc/passwd or /etc/group (a scratch image).
func notFound(err error) bool {
	var u user.UnknownUserError
	var g user.UnknownGroupError
	return errors.As(err, &u) || errors.As(err, &g) || errors.Is(err, fs.ErrNotExist)
}
