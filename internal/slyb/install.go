package slyb

import (
	"bufio"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nimling/nimbody/cli"
)

const extensionID = "slybrowser@slytech.no"

const prefsStart = "// slybrowser start"

const prefsEnd = "// slybrowser end"

var prefs = []string{
	`user_pref("extensions.experiments.enabled", true);`,
	`user_pref("xpinstall.signatures.required", false);`,
	`user_pref("extensions.autoDisableScopes", 0);`,
	`user_pref("extensions.startupScanScopes", 1);`,
	`user_pref("identity.fxaccounts.enabled", false);`,
	`user_pref("signon.rememberSignons", false);`,
	`user_pref("signon.autofillForms", false);`,
	`user_pref("signon.generation.enabled", false);`,
	`user_pref("signon.firefoxRelay.feature", "disabled");`,
	`user_pref("signon.management.page.breach-alerts.enabled", false);`,
	`user_pref("extensions.formautofill.addresses.enabled", false);`,
	`user_pref("extensions.formautofill.creditCards.enabled", false);`,
	`user_pref("browser.translations.enable", false);`,
	`user_pref("browser.translations.automaticallyPopup", false);`,
	`user_pref("services.sync.engine.spaces", false);`,
	`user_pref("zen.welcome-screen.seen", true);`,
	`user_pref("zen.view.use-single-toolbar", true);`,
	`user_pref("zen.view.sidebar-expanded", true);`,
	`user_pref("zen.view.show-newtab-button-top", true);`,
	`user_pref("zen.urlbar.replace-newtab", true);`,
	`user_pref("zen.urlbar.show-domain-only-in-sidebar", true);`,
	`user_pref("zen.theme.content-element-separation", 8);`,
	`user_pref("zen.theme.border-radius", 10);`,
	`user_pref("zen.theme.acrylic-sidebar", true);`,
	`user_pref("zen.tabs.show-newtab-vertical", false);`,
}

type Installed struct {
	Profile   string `json:"profile"`
	Extension string `json:"extension"`
	Socket    string `json:"socket"`
	Claude    string `json:"claude"`
	Rules     string `json:"rules"`
}

func GetProfile(zen string) (string, error) {
	ini, err := os.Open(filepath.Join(zen, "profiles.ini"))
	if err != nil {
		return "", cli.Rejected("no Zen profile under "+zen, "start Zen once so it creates its profile, quit it, then run `slyb install` again")
	}
	defer ini.Close()
	var installDefault, profileDefault, path string
	relative := true
	section := ""
	scanner := bufio.NewScanner(ini)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			section, path, relative = line, "", true
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		switch {
		case strings.HasPrefix(section, "[Install") && key == "Default":
			installDefault = value
		case strings.HasPrefix(section, "[Profile") && key == "Path":
			path = value
		case strings.HasPrefix(section, "[Profile") && key == "IsRelative":
			relative = value == "1"
		case strings.HasPrefix(section, "[Profile") && key == "Default" && value == "1":
			profileDefault = path
			if relative {
				profileDefault = filepath.Join(zen, path)
			}
		}
	}
	if installDefault != "" {
		if filepath.IsAbs(installDefault) {
			return installDefault, nil
		}
		return filepath.Join(zen, installDefault), nil
	}
	if profileDefault != "" {
		return profileDefault, nil
	}
	return "", cli.Rejected("profiles.ini under "+zen+" names no default profile", "pass --profile <dir>")
}

func PostInstall(profile string, browser fs.FS) (Installed, error) {
	home, err := Home()
	if err != nil {
		return Installed{}, err
	}
	socket, err := SocketPath()
	if err != nil {
		return Installed{}, err
	}
	root := filepath.Join(home, "browser")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Installed{}, err
	}
	err = fs.WalkDir(browser, "browser", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := fs.ReadFile(browser, path)
		if err != nil {
			return err
		}
		target := filepath.Join(root, strings.TrimPrefix(path, "browser/"))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o600)
	})
	if err != nil {
		return Installed{}, err
	}
	extensions := filepath.Join(profile, "extensions")
	if err := os.MkdirAll(extensions, 0o700); err != nil {
		return Installed{}, err
	}
	proxy := filepath.Join(extensions, extensionID)
	if err := os.WriteFile(proxy, []byte(root+"/"), 0o600); err != nil {
		return Installed{}, err
	}
	if err := putPrefs(filepath.Join(profile, "user.js")); err != nil {
		return Installed{}, err
	}
	executable, err := cli.Executable()
	if err != nil {
		return Installed{}, err
	}
	claude, _ := exec.LookPath("claude")
	if err := PutSettings(Settings{Slyb: executable, Claude: claude}); err != nil {
		return Installed{}, err
	}
	rules, err := organizationPath()
	if err != nil {
		return Installed{}, err
	}
	if _, err := os.Stat(rules); os.IsNotExist(err) {
		if err := os.WriteFile(rules, []byte(organizationSeed), 0o600); err != nil {
			return Installed{}, err
		}
	}
	return Installed{Profile: profile, Extension: proxy, Socket: socket, Claude: claude, Rules: rules}, nil
}

func putPrefs(path string) error {
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var kept []string
	inside := false
	for _, line := range strings.Split(strings.TrimRight(string(current), "\n"), "\n") {
		switch {
		case line == prefsStart:
			inside = true
		case line == prefsEnd:
			inside = false
		case !inside && (line != "" || len(kept) > 0):
			kept = append(kept, line)
		}
	}
	kept = append(kept, prefsStart)
	kept = append(kept, prefs...)
	kept = append(kept, prefsEnd)
	return os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o600)
}
