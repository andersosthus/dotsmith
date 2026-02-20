// Package identity provides OS, hostname, and username auto-detection for
// override layer resolution in dotsmith.
package identity

import (
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strings"
)

// OverrideLayer names an override precedence layer.
type OverrideLayer string

const (
	// LayerBase is the default base layer, always present.
	LayerBase OverrideLayer = "base"
	// LayerOS is the operating-system override layer.
	LayerOS OverrideLayer = "os"
	// LayerHostname is the hostname override layer.
	LayerHostname OverrideLayer = "hostname"
	// LayerUsername is the username override layer.
	LayerUsername OverrideLayer = "username"
	// LayerUserhost is the user@hostname override layer (highest precedence).
	LayerUserhost OverrideLayer = "userhost"
)

// LayerEntry pairs an override layer name with the directory key within that
// layer (e.g. layer="hostname", key="workstation").
type LayerEntry struct {
	Layer OverrideLayer
	Key   string
}

// Identity holds the auto-detected or configured identity values used to
// resolve override layers.
type Identity struct {
	// OS is the operating system (e.g. "linux", "darwin"). Defaults to
	// runtime.GOOS when auto-detected.
	OS string
	// Hostname is the short hostname with domain stripped.
	Hostname string
	// Username is the current OS user's login name.
	Username string
}

// DetectFunc is the function used to detect the current identity. It can be
// replaced in tests to inject a specific identity or error.
var DetectFunc = Detect

// hostnameFunc and userFunc are variables so tests can inject fakes.
var (
	hostnameFunc func() (string, error) = os.Hostname
	userFunc     func() (*user.User, error)
)

func init() {
	userFunc = user.Current
}

// Detect auto-detects the current identity from the OS environment.
// It returns an error if hostname or username cannot be determined.
func Detect() (Identity, error) {
	goos := runtime.GOOS

	hostname, err := hostnameFunc()
	if err != nil {
		return Identity{}, fmt.Errorf(
			"detect identity: get hostname: %w — set identity.hostname in .dotsmith.yml to override",
			err,
		)
	}

	u, err := userFunc()
	if err != nil {
		return Identity{}, fmt.Errorf(
			"detect identity: get current user: %w — set identity.username in .dotsmith.yml to override",
			err,
		)
	}

	return Identity{
		OS:       goos,
		Hostname: shortHostname(hostname),
		Username: u.Username,
	}, nil
}

// Layers returns the ordered list of override layer entries that apply to this
// identity, starting from base and ending at userhost. Layers with empty keys
// are omitted.
func (id Identity) Layers() []LayerEntry {
	entries := []LayerEntry{{Layer: LayerBase, Key: "base"}}

	if id.OS != "" {
		entries = append(entries, LayerEntry{Layer: LayerOS, Key: id.OS})
	}
	if id.Hostname != "" {
		entries = append(entries, LayerEntry{Layer: LayerHostname, Key: id.Hostname})
	}
	if id.Username != "" {
		entries = append(entries, LayerEntry{Layer: LayerUsername, Key: id.Username})
	}
	if id.Username != "" && id.Hostname != "" {
		entries = append(entries, LayerEntry{Layer: LayerUserhost, Key: id.Userhost()})
	}

	return entries
}

// Userhost returns the "username@hostname" compound key used for the userhost
// override layer.
func (id Identity) Userhost() string {
	return id.Username + "@" + id.Hostname
}

// shortHostname strips the domain suffix from a fully-qualified hostname.
func shortHostname(fqdn string) string {
	if idx := strings.IndexByte(fqdn, '.'); idx != -1 {
		return fqdn[:idx]
	}
	return fqdn
}
