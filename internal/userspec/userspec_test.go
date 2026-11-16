package userspec

import (
	"errors"
	"io/fs"
	"os/user"
	"reflect"
	"strings"
	"testing"
)

// fakeDB stands in for /etc/passwd and /etc/group, so the tests never read
// the host's user database. Missing files are modelled with fs.ErrNotExist,
// as os/user's pure-Go mode reports them.
type fakeDB struct {
	users     []*user.User
	groups    map[string]string   // name -> gid
	members   map[string][]string // username -> gids from the group file
	noPasswd  bool
	noGroup   bool
	listError error
}

func (db fakeDB) install(t *testing.T) {
	t.Helper()
	oldName, oldID, oldGroup, oldIDs := lookupUser, lookupUserID, lookupGroup, groupIDs
	t.Cleanup(func() { lookupUser, lookupUserID, lookupGroup, groupIDs = oldName, oldID, oldGroup, oldIDs })
	missing := &fs.PathError{Op: "open", Path: "/etc/passwd", Err: fs.ErrNotExist}
	lookupUser = func(name string) (*user.User, error) {
		if db.noPasswd {
			return nil, missing
		}
		for _, u := range db.users {
			if u.Username == name {
				return u, nil
			}
		}
		return nil, user.UnknownUserError(name)
	}
	lookupUserID = func(uid string) (*user.User, error) {
		if db.noPasswd {
			return nil, missing
		}
		for _, u := range db.users {
			if u.Uid == uid {
				return u, nil
			}
		}
		return nil, user.UnknownUserIdError(0)
	}
	lookupGroup = func(name string) (*user.Group, error) {
		if db.noGroup {
			return nil, &fs.PathError{Op: "open", Path: "/etc/group", Err: fs.ErrNotExist}
		}
		if gid, ok := db.groups[name]; ok {
			return &user.Group{Gid: gid, Name: name}, nil
		}
		return nil, user.UnknownGroupError(name)
	}
	groupIDs = func(u *user.User) ([]string, error) {
		if db.listError != nil {
			return nil, db.listError
		}
		if db.noGroup {
			return nil, &fs.PathError{Op: "open", Path: "/etc/group", Err: fs.ErrNotExist}
		}
		return append([]string{u.Gid}, db.members[u.Username]...), nil
	}
}

func testDB() fakeDB {
	return fakeDB{
		users: []*user.User{
			{Uid: "1000", Gid: "1000", Username: "kahi", HomeDir: "/home/kahi"},
			{Uid: "1001", Gid: "1001", Username: "app", HomeDir: "/srv/app"},
			{Uid: "33", Gid: "33", Username: "www-data", HomeDir: "/var/www"},
			{Uid: "0", Gid: "0", Username: "root", HomeDir: "/root"},
		},
		groups:  map[string]string{"kahi": "1000", "app": "1001", "www-data": "33", "adm": "4", "root": "0"},
		members: map[string][]string{"kahi": {"4"}, "root": {"0"}},
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		spec string
		want Credential
	}{
		// A name: passwd uid and primary gid, groups as initgroups computes them.
		{"kahi", Credential{Uid: 1000, Gid: 1000, Groups: []uint32{4, 1000}, Name: "kahi", Home: "/home/kahi", HasEntry: true}},
		{"www-data", Credential{Uid: 33, Gid: 33, Groups: []uint32{33}, Name: "www-data", Home: "/var/www", HasEntry: true}},
		// A group override sets the primary gid and is added to the list.
		{"kahi:adm", Credential{Uid: 1000, Gid: 4, Groups: []uint32{4, 1000}, Name: "kahi", Home: "/home/kahi", HasEntry: true, ExplicitGroup: true}},
		{"kahi:33", Credential{Uid: 1000, Gid: 33, Groups: []uint32{4, 33, 1000}, Name: "kahi", Home: "/home/kahi", HasEntry: true, ExplicitGroup: true}},
		// A numeric uid is a uid, and takes its passwd entry when there is one.
		{"1000", Credential{Uid: 1000, Gid: 1000, Groups: []uint32{4, 1000}, Name: "kahi", Home: "/home/kahi", HasEntry: true}},
		{"1000:adm", Credential{Uid: 1000, Gid: 4, Groups: []uint32{4, 1000}, Name: "kahi", Home: "/home/kahi", HasEntry: true, ExplicitGroup: true}},
		// Without an entry: gid defaults to the uid, groups are the gid alone.
		{"4242", Credential{Uid: 4242, Gid: 4242, Groups: []uint32{4242}}},
		{"4242:4243", Credential{Uid: 4242, Gid: 4243, Groups: []uint32{4243}, ExplicitGroup: true}},
		{"4242:adm", Credential{Uid: 4242, Gid: 4, Groups: []uint32{4}, ExplicitGroup: true}},
		// gid 0 the user is listed in is kept.
		{"root", Credential{Uid: 0, Gid: 0, Groups: []uint32{0}, Name: "root", Home: "/root", HasEntry: true}},
	}
	testDB().install(t)
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			got, err := Resolve(tt.spec)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.spec, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Resolve(%q) = %+v, want %+v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	testDB().install(t)
	for spec, want := range map[string]string{
		"nosuch":          "user not found: nosuch",
		"kahi:nosuch":     "group not found: nosuch",
		"4242:nosuch":     "group not found: nosuch",
		"":                `invalid user ""`,
		":adm":            `invalid user ":adm"`,
		"kahi:":           `invalid user "kahi:"`,
		"99999999999":     `invalid user "99999999999"`,
		"1000:9999999999": `invalid user "1000:9999999999"`,
	} {
		_, err := Resolve(spec)
		if err == nil || err.Error() != want {
			t.Errorf("Resolve(%q) error = %v, want %q", spec, err, want)
		}
	}
}

// A scratch container image may have no /etc/passwd or /etc/group: numeric
// ids still work, names are not found.
func TestResolveMissingFiles(t *testing.T) {
	db := testDB()
	db.noPasswd, db.noGroup = true, true
	db.install(t)
	got, err := Resolve("33")
	if err != nil || !reflect.DeepEqual(got, Credential{Uid: 33, Gid: 33, Groups: []uint32{33}}) {
		t.Errorf("Resolve(33) = %+v, %v; want uid/gid 33, groups [33]", got, err)
	}
	if _, err := Resolve("kahi"); err == nil || err.Error() != "user not found: kahi" {
		t.Errorf("Resolve(kahi) error = %v, want user not found", err)
	}
	if _, err := Resolve("33:adm"); err == nil || err.Error() != "group not found: adm" {
		t.Errorf("Resolve(33:adm) error = %v, want group not found", err)
	}
}

// A passwd without a group file: the entry's primary gid alone.
func TestResolveNoGroupFile(t *testing.T) {
	db := testDB()
	db.noGroup = true
	db.install(t)
	got, err := Resolve("kahi")
	if err != nil || !reflect.DeepEqual(got.Groups, []uint32{1000}) {
		t.Errorf("Resolve(kahi) = %+v, %v; want groups [1000]", got, err)
	}
}

// A group file that exists but cannot be read is an error, not "no groups":
// the drop must not silently lose groups the admin configured.
func TestResolveGroupListError(t *testing.T) {
	db := testDB()
	db.listError = errors.New("permission denied")
	db.install(t)
	_, err := Resolve("kahi")
	if err == nil || !strings.Contains(err.Error(), "cannot list groups of user kahi: permission denied") {
		t.Errorf("Resolve(kahi) error = %v", err)
	}
}

func TestMatches(t *testing.T) {
	c := Credential{Uid: 1000, Gid: 1000}
	if !c.Matches(1000, 1000) || !c.Matches(1000, 50) {
		t.Error("same uid without an explicit group should match")
	}
	if c.Matches(1001, 1000) {
		t.Error("different uid should not match")
	}
	g := Credential{Uid: 1000, Gid: 1000, ExplicitGroup: true}
	if !g.Matches(1000, 1000) || g.Matches(1000, 50) {
		t.Error("an explicit group must also match the gid")
	}
}
