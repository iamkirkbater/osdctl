package cmd

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/coreos/go-semver/semver"
	"github.com/spf13/cobra"
)

const (
	versionAPIEndpoint     = "https://api.github.com/repos/openshift/osdctl/releases/latest"
	versionAddressTemplate = "https://github.com/openshift/osdctl/releases/download/v%s/osdctl_%s_%s_%s.tar.gz" // version, version, GOOS, GOARCH
)

var (
	// GitCommit is the short git commit hash from the environment
	// Will be set during build process via GoReleaser
	// See also: https://pkg.go.dev/cmd/link
	GitCommit string

	// Version is the tag version from the environment
	// Will be set during build process via GoReleaser
	// See also: https://pkg.go.dev/cmd/link
	Version string
)

// githubResponse is a necessary struct for the JSON unmarshalling that is happening
// in the getLatestVersion().
type gitHubResponse struct {
	TagName string `json:"tag_name"`
}

// versionResponse is necessary for the JSON version response. It uses the three
// variables that get set during the build.
type versionResponse struct {
	Commit  string `json:"commit"`
	Version string `json:"version"`
	Latest  string `json:"latest"`
}

var upgradeCmd = &cobra.Command{
	Use:           "upgrade",
	Short:         "Upgrade osdctl",
	Long:          "Fetch latest osdctl from GitHub and replace the running binary",
	RunE:          upgrade,
	SilenceErrors: true,
}

func upgrade(cmd *cobra.Command, args []string) error {
	// rootName ensures that the upgrade will fail if we ever decide to rename osdctl
	// between releases :-)
	rootName := cmd.Root().Name()

	latest, err := getLatestVersion()
	if err != nil {
		return err
	}
	latestWithoutPrefix := strings.TrimPrefix(latest, "v")
	currentSemVer := semver.New(Version)
	latestSemVer := semver.New(latestWithoutPrefix)
	if !currentSemVer.LessThan(*latestSemVer) {
		fmt.Println("Already up to date, nothing to do!")
		return nil
	}
	// upgrade necessary
	client := http.Client{
		Timeout: time.Second * 60,
	}

	addr := fmt.Sprintf(versionAddressTemplate,
		latestWithoutPrefix,
		latestWithoutPrefix,
		parseGOOS(runtime.GOOS),
		parseGOARCH(runtime.GOARCH))

	req, err := http.NewRequest(http.MethodGet, addr, nil)
	if err != nil {
		return err
	}

	res, err := client.Do(req)
	if err != nil {
		return err
	}

	gzf, err := gzip.NewReader(res.Body)
	if err != nil {
		return err
	}

	tr := tar.NewReader(gzf)
	for {
		f, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if f.Name != rootName {
			continue
		}
		// For replacing a running executable we have to use the syscall "rename".
		// "rename" can only be called on executables (old/new destination/name)
		// that are stored on the same filesystem. This is the reason, why we cannot
		// use a directory on ramfs here (f.e. /tmp/). Instead, we are creating a
		// temp dir in the user's $HOME.
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return err
		}

		dir, err := ioutil.TempDir(homeDir, ".*")
		if err != nil {
			return err
		}

		defer func(path string) {
			err := os.RemoveAll(path)
			if err != nil {
				fmt.Println("Error removing directory ", path)
			}
		}(dir)

		tmpFilePath := filepath.Join(dir, rootName)

		tmpFile, err := os.OpenFile(tmpFilePath, os.O_CREATE|os.O_RDWR, 0700) //#nosec G304|G302 -- tmpFilePath cannot be constant
		if err != nil {
			return err
		}

		_, err = io.Copy(tmpFile, tr) //#nosec G110 -- source is trusted, so decompression bomb is unlikely
		if err != nil {
			return err
		}

		// get path of current executable
		exe, err := os.Executable()
		if err != nil {
			return err
		}

		err = os.Rename(tmpFilePath, filepath.Join(filepath.Dir(exe), rootName))
		if err != nil {
			return err
		}
	}
	return nil
}

func parseGOOS(goos string) string {
	switch goos {
	case "linux":
		return "Linux"
	case "darwin":
		return "Darwin"
	case "windows":
		return "Windows"
	default:
		return ""
	}
}

func parseGOARCH(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	default:
		return ""
	}
}

// getLatestVersion connects to the GitHub API and returns the latest osdctl tag name
// Interesting Note: GitHub only shows the latest "stable" tag. This means, that
// tags with a suffix like *-rc.1 are not returned. We will always show the latest stable on master branch.
func getLatestVersion() (latest string, err error) {
	client := http.Client{
		Timeout: time.Second * 2,
	}

	req, err := http.NewRequest(http.MethodGet, versionAPIEndpoint, nil)
	if err != nil {
		return latest, err
	}

	res, err := client.Do(req)
	if err != nil {
		return latest, err
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return latest, err
	}

	githubResp := gitHubResponse{}
	err = json.Unmarshal(body, &githubResp)
	if err != nil {
		return latest, err
	}

	return githubResp.TagName, nil
}
