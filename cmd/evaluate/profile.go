package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// profile is a named model endpoint: any host serving POST /v1/systemone, such
// as TypeSafe, d1 or clm-serve, or OpenRouter's Decisions router.
type profile struct {
	Model   string `json:"model"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

type profiles struct {
	Active   string             `json:"active,omitempty"`
	Profiles map[string]profile `json:"profiles"`
}

func profilesPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "evaluate", "profiles.json"), nil
}

// loadProfiles returns an empty set, not an error, when there is no file yet.
func loadProfiles() (*profiles, string, error) {
	path, err := profilesPath()
	if err != nil {
		return nil, "", err
	}
	p := &profiles{}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, p); err != nil {
			return nil, "", fmt.Errorf("parsing %s: %w", path, err)
		}
	}
	if p.Profiles == nil {
		p.Profiles = map[string]profile{}
	}
	return p, path, nil
}

// save writes a 0600 temp file (the file holds API keys) and renames it, so a
// server starting mid-write never reads a partial file.
func (p *profiles) save(path string) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".profiles-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// selectedProfile returns the profile named by TYPESAFE_PROFILE, else the active one,
// else nil so route falls back to the environment variables.
func selectedProfile() (*profile, string, error) {
	// No config dir (HOME unset) means no profiles, so an env-only client
	// config keeps working; a pinned profile still has to be found.
	if _, err := os.UserConfigDir(); err != nil && os.Getenv("TYPESAFE_PROFILE") == "" {
		return nil, "", nil
	}
	p, path, err := loadProfiles()
	if err != nil {
		return nil, "", err
	}
	name := cmp.Or(os.Getenv("TYPESAFE_PROFILE"), p.Active)
	if name == "" {
		return nil, "", nil
	}
	pr, ok := p.Profiles[name]
	if !ok {
		return nil, "", fmt.Errorf("profile %q not found in %s", name, path)
	}
	return &pr, name, nil
}

// systemOneURL turns a host-level base into the full endpoint. Parse accepts a
// bare host as a relative URL, so the scheme and host carry the check.
// OpenRouter serves the same body at its own path, not /v1/systemone, so a base
// on its host is kept as given (a bare host gets the Decisions path); only the
// host can tell it apart, since Liquid's base also ends in /decisions. A base
// that already ends in /v1/decisions is kept too.
func systemOneURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("must be an absolute http(s) URL, got %q", base)
	}
	if strings.EqualFold(u.Hostname(), "openrouter.ai") {
		if strings.Trim(u.Path, "/") == "" {
			return openRouterURL, nil
		}
		return u.String(), nil
	}
	if strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1/decisions") {
		return u.String(), nil
	}
	return u.JoinPath("v1", "systemone").String(), nil
}

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage named model profiles",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	var pr profile
	add := &cobra.Command{
		Use:   "add NAME",
		Short: "Add or replace a profile; the first one added becomes active",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pr.APIKey = cmp.Or(pr.APIKey, os.Getenv("TYPESAFE_API_KEY"))
			if pr.APIKey == "" {
				return errors.New("set --api-key or TYPESAFE_API_KEY")
			}
			if _, err := systemOneURL(pr.BaseURL); err != nil {
				return fmt.Errorf("--base-url %w", err)
			}
			p, path, err := loadProfiles()
			if err != nil {
				return err
			}
			p.Profiles[args[0]] = pr
			p.Active = cmp.Or(p.Active, args[0])
			return p.save(path)
		},
	}
	add.Flags().StringVar(&pr.Model, "model", "jev-latest", "default model for this profile")
	add.Flags().StringVar(&pr.BaseURL, "base-url", "https://api.typesafe.ai", "host serving /v1/systemone, or a full /v1/decisions or OpenRouter Decisions URL")
	add.Flags().StringVar(&pr.APIKey, "api-key", "", "API key (default $TYPESAFE_API_KEY)")

	cmd.AddCommand(
		add,
		&cobra.Command{
			Use:   "use NAME",
			Short: "Make a profile active; clients pick it up when they next start the server",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				p, path, err := loadProfiles()
				if err != nil {
					return err
				}
				if _, ok := p.Profiles[args[0]]; !ok {
					return fmt.Errorf("profile %q not found", args[0])
				}
				p.Active = args[0]
				return p.save(path)
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List profiles; * marks the active one",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				p, _, err := loadProfiles()
				if err != nil {
					return err
				}
				for _, name := range slices.Sorted(maps.Keys(p.Profiles)) {
					mark := " "
					if name == p.Active {
						mark = "*"
					}
					pr := p.Profiles[name]
					fmt.Fprintf(cmd.OutOrStdout(), "%s %s\t%s\t%s\n", mark, name, pr.Model, pr.BaseURL)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "remove NAME",
			Short: "Delete a profile",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				p, path, err := loadProfiles()
				if err != nil {
					return err
				}
				if _, ok := p.Profiles[args[0]]; !ok {
					return fmt.Errorf("profile %q not found", args[0])
				}
				delete(p.Profiles, args[0])
				if p.Active == args[0] {
					p.Active = ""
				}
				return p.save(path)
			},
		},
	)
	return cmd
}
