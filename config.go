package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type ExtraCredConfig struct {
	Name      string `json:"name"`
	Extension string `json:"extension"`
	Prefix    string `json:"prefix"`
}

type Config struct {
	ExtraCreds []ExtraCredConfig `json:"extra_creds"`
}

type CredentialFile struct {
	Path        string
	Type        string
	DisplayName string
	CredPrefix  string
	Extension   string
}

type Credentials struct {
	AuthURL                     string
	Username                    string
	Password                    string
	UserDomainName              string
	UserDomainId                string
	Region                      string
	TOTPCode                    string
	TOTPRequired                bool
	ProjectID                   string
	ProjectName                 string
	SystemScope                 string
	ApplicationCredentialID     string
	ApplicationCredentialSecret string
}

func getConfigDir() string {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		homeDir, _ := os.UserHomeDir()
		configDir = filepath.Join(homeDir, ".config")
	}
	return filepath.Join(configDir, "oscreds")
}

func loadConfigFile() *Config {
	configPath := filepath.Join(getConfigDir(), "config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		debugf("No config file found: %v\n", err)
		return &Config{}
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		debugf("Failed to parse config file: %v\n", err)
		return &Config{}
	}

	debugf("Loaded config with %d extra credential types\n", len(config.ExtraCreds))
	return &config
}

func getPassDir() string {
	passDir := os.Getenv("PASSWORD_STORE_DIR")
	if passDir == "" {
		homeDir, _ := os.UserHomeDir()
		passDir = filepath.Join(homeDir, ".password-store")
	}
	return passDir
}

func scanPassDir(passDir, extension, credType, credPrefix string, credFiles *[]CredentialFile) error {
	suffix := extension + ".gpg"

	err := filepath.Walk(passDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if !info.IsDir() && strings.HasSuffix(info.Name(), suffix) {
			relPath, err := filepath.Rel(passDir, path)
			if err != nil {
				return nil
			}

			passPath := strings.TrimSuffix(relPath, ".gpg")
			passPath = filepath.ToSlash(passPath)

			displayName := strings.TrimSuffix(passPath, extension)

			*credFiles = append(*credFiles, CredentialFile{
				Path:        passPath,
				Type:        credType,
				DisplayName: displayName,
				CredPrefix:  credPrefix,
				Extension:   extension,
			})
		}

		return nil
	})

	return err
}

func GetPassCredFiles(config *Config) ([]CredentialFile, error) {
	passDir := getPassDir()

	var credFiles []CredentialFile

	err := scanPassDir(passDir, ".openrc", "openrc", "OS", &credFiles)
	if err != nil {
		return nil, err
	}

	for _, extra := range config.ExtraCreds {
		ext := extra.Extension
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		err := scanPassDir(passDir, ext, extra.Name, extra.Prefix, &credFiles)
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(credFiles, func(i, j int) bool {
		if credFiles[i].Type != credFiles[j].Type {
			return credFiles[i].Type < credFiles[j].Type
		}
		return credFiles[i].DisplayName < credFiles[j].DisplayName
	})
	return credFiles, nil
}

func FindCredentialFile(credFiles []CredentialFile, pathOrName string) CredentialFile {
	if idx := strings.Index(pathOrName, ":"); idx > 0 {
		typeName := pathOrName[:idx]
		displayName := pathOrName[idx+1:]
		for _, cf := range credFiles {
			if cf.Type == typeName && cf.DisplayName == displayName {
				return cf
			}
		}
	}

	searchPath := pathOrName
	for _, ext := range allExtensions() {
		searchPath = strings.TrimSuffix(searchPath, ext)
	}

	for _, cf := range credFiles {
		if cf.DisplayName == searchPath {
			return cf
		}
		if cf.Path == pathOrName || cf.Path == searchPath+cf.Extension {
			return cf
		}
	}

	return CredentialFile{}
}

func allExtensions() []string {
	config := loadConfigFile()
	extensions := []string{".openrc"}
	for _, extra := range config.ExtraCreds {
		extensions = append(extensions, extra.Extension)
	}
	return extensions
}

func LoadCredentials(credFile CredentialFile) (*Credentials, error) {
	decryptedText, err := passShow(credFile.Path)
	if err != nil {
		return nil, err
	}

	creds := &Credentials{}
	lines := strings.Split(decryptedText, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "export "); ok {
			line = after
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"'")

		switch key {
		case "OS_AUTH_URL":
			creds.AuthURL = value
		case "OS_USERNAME":
			creds.Username = value
		case "OS_PASSWORD":
			creds.Password = value
		case "OS_USER_DOMAIN_NAME":
			creds.UserDomainName = value
		case "OS_USER_DOMAIN_ID":
			creds.UserDomainId = value
		case "OS_REGION_NAME":
			creds.Region = value
		case "OS_PROJECT_ID":
			creds.ProjectID = value
		case "OS_PROJECT_NAME":
			creds.ProjectName = value
		case "OS_SYSTEM_SCOPE":
			creds.SystemScope = value
		case "OS_TOTP_REQUIRED":
			creds.TOTPRequired = strings.ToLower(value) == "true" || value == "1"
		case "OS_APPLICATION_CREDENTIAL_ID":
			creds.ApplicationCredentialID = value
		case "OS_APPLICATION_CREDENTIAL_SECRET":
			creds.ApplicationCredentialSecret = value
		}
	}

	// Set default user domain name if neither name nor ID is set
	if creds.UserDomainName == "" && creds.UserDomainId == "" {
		creds.UserDomainName = "Default"
	}

	return creds, nil
}

func passShow(entry string) (string, error) {
	cmd := exec.Command("pass", "show", entry)
	cmd.Env = withPasswordStoreDir(os.Environ(), getPassDir())

	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg != "" {
			msg = ": " + msg
		}
		return "", fmt.Errorf("pass show %q failed: %w%s", entry, err, msg)
	}

	return string(output), nil
}

func withPasswordStoreDir(env []string, passDir string) []string {
	const key = "PASSWORD_STORE_DIR="
	out := make([]string, 0, len(env)+1)
	found := false
	for _, item := range env {
		if strings.HasPrefix(item, key) {
			out = append(out, key+passDir)
			found = true
			continue
		}
		out = append(out, item)
	}
	if !found {
		out = append(out, key+passDir)
	}
	return out
}

// HasProjectDefined returns true if the credentials have a project already specified
func (c *Credentials) HasProjectDefined() bool {
	return c.ProjectID != "" || c.ProjectName != ""
}

// IsApplicationCredential returns true if the credentials use application credential authentication
func (c *Credentials) IsApplicationCredential() bool {
	return c.ApplicationCredentialID != "" && c.ApplicationCredentialSecret != ""
}

func LoadRawCredentials(credFile CredentialFile) (string, error) {
	decryptedText, err := passShow(credFile.Path)
	if err != nil {
		return "", err
	}

	var exports []string
	lines := strings.Split(decryptedText, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "export ") {
			exports = append(exports, line)
		}
	}

	return strings.Join(exports, "\n"), nil
}

type LoadedEntry struct {
	Type    string
	Prefix  string
	Display string
}

func parseLoadedEntries(loaded string) []LoadedEntry {
	if loaded == "" {
		return nil
	}

	var entries []LoadedEntry
	for _, entry := range strings.Split(loaded, ",") {
		parts := strings.SplitN(entry, "|", 3)
		if len(parts) != 3 {
			continue
		}
		entries = append(entries, LoadedEntry{
			Type:    parts[0],
			Prefix:  parts[1],
			Display: parts[2],
		})
	}
	return entries
}

func buildLoadedEntries(newType, newPrefix, newDisplay string) string {
	currentLoaded := os.Getenv("__OSCREDS_LOADED")
	newEntry := newType + "|" + newPrefix + "|" + newDisplay

	var entries []string
	if currentLoaded != "" {
		for _, entry := range strings.Split(currentLoaded, ",") {
			parts := strings.SplitN(entry, "|", 2)
			if len(parts) >= 1 && parts[0] != newType {
				entries = append(entries, entry)
			}
		}
	}

	entries = append(entries, newEntry)
	return strings.Join(entries, ",")
}
