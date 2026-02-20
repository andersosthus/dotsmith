package identity

import (
	"errors"
	"os/user"
	"testing"
)

func TestLayerConstants(t *testing.T) {
	if LayerBase != "base" {
		t.Errorf("LayerBase = %q, want %q", LayerBase, "base")
	}
	if LayerOS != "os" {
		t.Errorf("LayerOS = %q, want %q", LayerOS, "os")
	}
	if LayerHostname != "hostname" {
		t.Errorf("LayerHostname = %q, want %q", LayerHostname, "hostname")
	}
	if LayerUsername != "username" {
		t.Errorf("LayerUsername = %q, want %q", LayerUsername, "username")
	}
	if LayerUserhost != "userhost" {
		t.Errorf("LayerUserhost = %q, want %q", LayerUserhost, "userhost")
	}
}

func TestUserhost(t *testing.T) {
	tests := []struct {
		name string
		id   Identity
		want string
	}{
		{
			name: "both fields set",
			id:   Identity{Username: "anders", Hostname: "workstation"},
			want: "anders@workstation",
		},
		{
			name: "empty username",
			id:   Identity{Username: "", Hostname: "workstation"},
			want: "@workstation",
		},
		{
			name: "empty hostname",
			id:   Identity{Username: "anders", Hostname: ""},
			want: "anders@",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.id.Userhost()
			if got != tc.want {
				t.Errorf("Userhost() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLayers(t *testing.T) {
	tests := []struct {
		name string
		id   Identity
		want []LayerEntry
	}{
		{
			name: "full identity",
			id:   Identity{OS: "linux", Hostname: "workstation", Username: "anders"},
			want: []LayerEntry{
				{Layer: LayerBase, Key: "base"},
				{Layer: LayerOS, Key: "linux"},
				{Layer: LayerHostname, Key: "workstation"},
				{Layer: LayerUsername, Key: "anders"},
				{Layer: LayerUserhost, Key: "anders@workstation"},
			},
		},
		{
			name: "only OS set",
			id:   Identity{OS: "darwin"},
			want: []LayerEntry{
				{Layer: LayerBase, Key: "base"},
				{Layer: LayerOS, Key: "darwin"},
			},
		},
		{
			name: "only hostname set",
			id:   Identity{Hostname: "laptop"},
			want: []LayerEntry{
				{Layer: LayerBase, Key: "base"},
				{Layer: LayerHostname, Key: "laptop"},
			},
		},
		{
			name: "only username set",
			id:   Identity{Username: "root"},
			want: []LayerEntry{
				{Layer: LayerBase, Key: "base"},
				{Layer: LayerUsername, Key: "root"},
			},
		},
		{
			name: "username and hostname but no OS",
			id:   Identity{Hostname: "box", Username: "alice"},
			want: []LayerEntry{
				{Layer: LayerBase, Key: "base"},
				{Layer: LayerHostname, Key: "box"},
				{Layer: LayerUsername, Key: "alice"},
				{Layer: LayerUserhost, Key: "alice@box"},
			},
		},
		{
			name: "empty identity",
			id:   Identity{},
			want: []LayerEntry{
				{Layer: LayerBase, Key: "base"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.id.Layers()
			if len(got) != len(tc.want) {
				t.Fatalf("Layers() len = %d, want %d; got %v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Layers()[%d] = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestShortHostname(t *testing.T) {
	tests := []struct {
		fqdn string
		want string
	}{
		{"workstation", "workstation"},
		{"workstation.example.com", "workstation"},
		{"host.a.b.c", "host"},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.fqdn, func(t *testing.T) {
			got := shortHostname(tc.fqdn)
			if got != tc.want {
				t.Errorf("shortHostname(%q) = %q, want %q", tc.fqdn, got, tc.want)
			}
		})
	}
}

func TestDetect_Success(t *testing.T) {
	origHostname := hostnameFunc
	origUser := userFunc
	t.Cleanup(func() {
		hostnameFunc = origHostname
		userFunc = origUser
	})

	hostnameFunc = func() (string, error) { return "workstation.local", nil }
	userFunc = func() (*user.User, error) {
		return &user.User{Username: "testuser"}, nil
	}

	id, err := Detect()
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if id.Hostname != "workstation" {
		t.Errorf("Hostname = %q, want %q", id.Hostname, "workstation")
	}
	if id.Username != "testuser" {
		t.Errorf("Username = %q, want %q", id.Username, "testuser")
	}
	if id.OS == "" {
		t.Error("OS should be non-empty (runtime.GOOS)")
	}
}

func TestDetect_HostnameError(t *testing.T) {
	origHostname := hostnameFunc
	t.Cleanup(func() { hostnameFunc = origHostname })

	hostnameFunc = func() (string, error) { return "", errors.New("no hostname") }

	_, err := Detect()
	if err == nil {
		t.Fatal("expected error from Detect(), got nil")
	}
}

func TestDetect_UserError(t *testing.T) {
	origHostname := hostnameFunc
	origUser := userFunc
	t.Cleanup(func() {
		hostnameFunc = origHostname
		userFunc = origUser
	})

	hostnameFunc = func() (string, error) { return "host", nil }
	userFunc = func() (*user.User, error) { return nil, errors.New("no user") }

	_, err := Detect()
	if err == nil {
		t.Fatal("expected error from Detect(), got nil")
	}
}
