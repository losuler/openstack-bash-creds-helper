package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

var debugMode bool

var shellType string

var projectName string

func debugf(format string, args ...interface{}) {
	if debugMode {
		fmt.Fprintf(os.Stderr, "DEBUG: "+format, args...)
	}
}

func main() {
	debug := flag.Bool("debug", false, "Enable debug output")
	flag.StringVar(&shellType, "shell", "bash", "Shell type for output format (bash or fish)")
	flag.StringVar(&projectName, "project", "", "Project name to scope to (skips interactive selection)")
	remove := flag.Bool("remove", false, "Remove loaded credentials (shows fzf selector if multiple)")
	flag.Parse()

	if shellType != "bash" && shellType != "fish" {
		fmt.Fprintf(os.Stderr, "Error: unsupported shell type %q (use bash or fish)\n", shellType)
		os.Exit(1)
	}

	debugMode = *debug
	DebugMode = *debug

	if *remove {
		handleRemove()
		return
	}

	config := loadConfigFile()

	credFiles, err := GetPassCredFiles(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting credential files: %v\n", err)
		os.Exit(1)
	}

	if len(credFiles) == 0 {
		fmt.Fprintf(os.Stderr, "No credential files found in pass\n")
		os.Exit(1)
	}

	var credFile CredentialFile
	if flag.NArg() > 0 {
		credPath := flag.Arg(0)
		credFile = FindCredentialFile(credFiles, credPath)
		if credFile.Path == "" {
			fmt.Fprintf(os.Stderr, "Credential file not found: %s\n", credPath)
			os.Exit(1)
		}
	} else {
		credFile = SelectCredentialFile(credFiles)
		if credFile.Path == "" {
			fmt.Fprintf(os.Stderr, "No credential file selected\n")
			os.Exit(1)
		}
	}

	debugf("Loading credentials from %s (type: %s, prefix: %s)\n", credFile.Path, credFile.Type, credFile.CredPrefix)

	if credFile.Type != "openrc" {
		outputUnsetForPrefix(credFile.CredPrefix)
		rawExports, err := LoadRawCredentials(credFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading credentials from %s: %v\n", credFile.Path, err)
			os.Exit(1)
		}
		debugf("Loaded raw credentials (%d bytes)\n", len(rawExports))
		fmt.Println(rawExports)
		outputVar("__OSCREDS_LOADED", buildLoadedEntries(credFile.Type, credFile.CredPrefix, credFile.DisplayName))
		outputVar("__OSCREDS_LAST", credFile.Type+": "+credFile.DisplayName)
		return
	}

	creds, err := LoadCredentials(credFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading credentials from %s: %v\n", credFile.Path, err)
		os.Exit(1)
	}
	if creds.IsApplicationCredential() {
		debugf("Loaded application credentials - ID: %s, AuthURL: %s\n", creds.ApplicationCredentialID, creds.AuthURL)
	} else {
		debugf("Loaded credentials - Username: %s, AuthURL: %s, TOTPRequired: %v\n", creds.Username, creds.AuthURL, creds.TOTPRequired)
	}
	if creds.ProjectID != "" {
		debugf("ProjectID defined: %s\n", creds.ProjectID)
	}
	if creds.ProjectName != "" {
		debugf("ProjectName defined: %s\n", creds.ProjectName)
	}
	if creds.SystemScope != "" {
		debugf("SystemScope defined: %s\n", creds.SystemScope)
	}

	if !creds.IsApplicationCredential() && creds.TOTPRequired {
		debugf("TOTP required, prompting user\n")
		totpCode, err := PromptForTOTP()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading TOTP code: %v\n", err)
			os.Exit(1)
		}
		creds.TOTPCode = totpCode
		debugf("TOTP code entered (length: %d)\n", len(totpCode))
	} else if creds.IsApplicationCredential() {
		debugf("Application credentials - TOTP not applicable\n")
	} else {
		debugf("TOTP not required\n")
	}

	if creds.IsApplicationCredential() {
		debugf("Application credentials detected - getting pre-scoped token\n")

		token, tokenResponse, err := GetApplicationCredentialToken(creds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting application credential token: %v\n", err)
			os.Exit(1)
		}

		debugf("Successfully got application credential token\n")
		selectedProject := &Project{
			ID:   tokenResponse.Token.Project.ID,
			Name: tokenResponse.Token.Project.Name,
		}
		outputEnvironmentVars(credFile, selectedProject, token, creds)
		return
	}

	if creds.SystemScope != "" {
		debugf("System scope defined - getting unscoped token only\n")

		token, err := GetUnscopedToken(creds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting unscoped token: %v\n", err)
			os.Exit(1)
		}

		debugf("Successfully got unscoped token for system scope\n")
		outputSystemScopeVars(credFile, token, creds)
		return
	}

	if projectName != "" {
		debugf("Project specified via -project flag: %s\n", projectName)

		var scopedToken string
		var selectedProject *Project
		var err error

		var tokenResponse *TokenResponse
		scopedToken, tokenResponse, err = GetScopedTokenByProjectName(creds, projectName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting scoped token for project %q: %v\n", projectName, err)
			os.Exit(1)
		}
		selectedProject = &Project{
			ID:   tokenResponse.Token.Project.ID,
			Name: tokenResponse.Token.Project.Name,
		}

		credFile.DisplayName = credFile.DisplayName + "/" + selectedProject.Name
		debugf("Successfully got scoped token for project: %s\n", selectedProject.Name)
		outputEnvironmentVars(credFile, selectedProject, scopedToken, creds)
		return
	}

	if creds.HasProjectDefined() {
		debugf("Project defined in credentials - getting scoped token directly\n")

		var scopedToken string
		var selectedProject *Project
		var err error

		if creds.ProjectID != "" {
			debugf("Using ProjectID: %s\n", creds.ProjectID)
			scopedToken, err = GetScopedToken(creds, creds.ProjectID)
			selectedProject = &Project{ID: creds.ProjectID, Name: creds.ProjectName}
		} else {
			debugf("Using ProjectName: %s\n", creds.ProjectName)
			var tokenResponse *TokenResponse
			scopedToken, tokenResponse, err = GetScopedTokenByProjectName(creds, creds.ProjectName)
			if err == nil {
				selectedProject = &Project{
					ID:   tokenResponse.Token.Project.ID,
					Name: tokenResponse.Token.Project.Name,
				}
			}
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting scoped token: %v\n", err)
			os.Exit(1)
		}

		debugf("Successfully got scoped token for project: %s\n", selectedProject.Name)
		outputEnvironmentVars(credFile, selectedProject, scopedToken, creds)
		return
	}

	debugf("No project defined, need to list projects for user selection\n")

	var projectsList []Project

	debugf("Getting unscoped token to list projects\n")
	token, err := GetUnscopedToken(creds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting unscoped token: %v\n", err)
		os.Exit(1)
	}

	debugf("Got unscoped token, listing projects\n")
	projectsList, err = ListProjects(creds.AuthURL, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing projects: %v\n", err)
		os.Exit(1)
	}
	debugf("Found %d projects\n", len(projectsList))

	if len(projectsList) == 0 {
		fmt.Fprintf(os.Stderr, "No projects found\n")
		os.Exit(1)
	}

	selectedProject := SelectProject(projectsList, credFile)
	if selectedProject == nil {
		fmt.Fprintf(os.Stderr, "No project selected\n")
		os.Exit(1)
	}

	credFile.DisplayName = credFile.DisplayName + "/" + selectedProject.Name

	scopedToken, err := GetScopedToken(creds, selectedProject.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting scoped token: %v\n", err)
		os.Exit(1)
	}

	outputEnvironmentVars(credFile, selectedProject, scopedToken, creds)
}

func handleRemove() {
	loaded := os.Getenv("__OSCREDS_LOADED")
	entries := parseLoadedEntries(loaded)

	if len(entries) == 0 {
		os_isset := false
		for _, envVar := range os.Environ() {
			if strings.HasPrefix(envVar, "OS_") {
				os_isset = true
				break
			}
		}
		if os_isset {
			outputUnsetForPrefix("OS")
			outputUnsetVar("__OSCREDS_LOADED")
			outputUnsetVar("__OSCREDS_LAST")
		}
		return
	}

	if len(entries) == 1 {
		outputRemoveEntry(entries[0], entries)
		return
	}

	selected, ok := fzfSelect("Remove credentials:", entries, func(item LoadedEntry) string {
		return item.Type + ": " + item.Display
	})
	if !ok {
		return
	}

	outputRemoveEntry(selected, entries)
}

func outputRemoveEntry(entry LoadedEntry, allEntries []LoadedEntry) {
	outputUnsetForPrefix(entry.Prefix)
	outputUnsetVar("__OSCREDS_LAST")

	var remaining []string
	for _, e := range allEntries {
		if e.Type != entry.Type {
			remaining = append(remaining, e.Type+"|"+e.Prefix+"|"+e.Display)
		}
	}

	if len(remaining) == 0 {
		outputUnsetVar("__OSCREDS_LOADED")
	} else {
		outputVar("__OSCREDS_LOADED", strings.Join(remaining, ","))
	}
}

func outputUnsetForPrefix(prefix string) {
	for _, envVar := range os.Environ() {
		parts := strings.SplitN(envVar, "=", 2)
		name := parts[0]
		if strings.HasPrefix(name, prefix+"_") {
			outputUnsetVar(name)
		}
	}
}

func outputUnsetVar(name string) {
	if shellType == "fish" {
		fmt.Printf("set -eg %s\n", name)
	} else {
		fmt.Printf("unset %s\n", name)
	}
}

func fishEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

func outputVar(name string, value string) {
	if shellType == "fish" {
		fmt.Printf("set -gx %s %s\n", name, fishEscape(value))
	} else {
		escaped := strings.ReplaceAll(value, "'", "'\\''")
		fmt.Printf("export %s='%s'\n", name, escaped)
	}
}

func outputEnvironmentVars(credFile CredentialFile, project *Project, token string, creds *Credentials) {
	outputUnsetForPrefix("OS")
	outputVar("OS_CRED", credFile.DisplayName)
	outputVar("OS_IDENTITY_API_VERSION", "3")
	outputVar("OS_AUTH_URL", creds.AuthURL)
	outputVar("OS_PROJECT_ID", project.ID)
	outputVar("OS_TOKEN", token)
	outputVar("OS_AUTH_TYPE", "token")
	if creds.Region != "" {
		outputVar("OS_REGION_NAME", creds.Region)
	}
	outputVar("__OSCREDS_LOADED", buildLoadedEntries("openrc", "OS", credFile.DisplayName))
	outputVar("__OSCREDS_LAST", "openrc: "+credFile.DisplayName)
}

func outputSystemScopeVars(credFile CredentialFile, token string, creds *Credentials) {
	outputUnsetForPrefix("OS")
	outputVar("OS_CRED", credFile.DisplayName+"/system")
	outputVar("OS_IDENTITY_API_VERSION", "3")
	outputVar("OS_AUTH_URL", creds.AuthURL)
	outputVar("OS_SYSTEM_SCOPE", creds.SystemScope)
	outputVar("OS_TOKEN", token)
	outputVar("OS_AUTH_TYPE", "token")
	if creds.Region != "" {
		outputVar("OS_REGION_NAME", creds.Region)
	}
	outputVar("__OSCREDS_LOADED", buildLoadedEntries("openrc", "OS", credFile.DisplayName+"/system"))
	outputVar("__OSCREDS_LAST", "openrc: "+credFile.DisplayName+"/system")
}
