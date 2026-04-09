// Package security provides pre-dispatch threat detection for the ag3nts
// orchestrator. Patterns extracted from Goose's security/patterns.rs and
// adapted for Go regex syntax.
package security

import (
	"regexp"
)

// RiskLevel classifies the severity of a detected threat.
type RiskLevel int

const (
	RiskLow RiskLevel = iota
	RiskMedium
	RiskHigh
	RiskCritical
)

// String returns the human-readable name.
func (r RiskLevel) String() string {
	switch r {
	case RiskLow:
		return "low"
	case RiskMedium:
		return "medium"
	case RiskHigh:
		return "high"
	case RiskCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// Confidence returns a score 0-1 for this risk level.
func (r RiskLevel) Confidence() float64 {
	switch r {
	case RiskCritical:
		return 0.95
	case RiskHigh:
		return 0.75
	case RiskMedium:
		return 0.60
	case RiskLow:
		return 0.45
	default:
		return 0.0
	}
}

// ThreatCategory classifies the type of threat.
type ThreatCategory int

const (
	CatFileSystemDestruction ThreatCategory = iota
	CatRemoteCodeExecution
	CatDataExfiltration
	CatSystemModification
	CatNetworkAccess
	CatPrivilegeEscalation
	CatCommandInjection
)

// String returns the human-readable name.
func (c ThreatCategory) String() string {
	switch c {
	case CatFileSystemDestruction:
		return "filesystem_destruction"
	case CatRemoteCodeExecution:
		return "remote_code_execution"
	case CatDataExfiltration:
		return "data_exfiltration"
	case CatSystemModification:
		return "system_modification"
	case CatNetworkAccess:
		return "network_access"
	case CatPrivilegeEscalation:
		return "privilege_escalation"
	case CatCommandInjection:
		return "command_injection"
	default:
		return "unknown"
	}
}

// ThreatPattern is a compiled regex threat detection rule.
type ThreatPattern struct {
	Name        string
	Compiled    *regexp.Regexp
	Description string
	Risk        RiskLevel
	Category    ThreatCategory
}

// PatternMatch represents a detected threat in input text.
type PatternMatch struct {
	Pattern     *ThreatPattern
	MatchedText string
}

// patterns holds all compiled threat patterns. Initialized once at package load.
// Extracted from Goose crates/goose/src/security/patterns.rs with adaptations
// for Go regex (no lookaheads, PCRE features replaced with alternatives).
var patterns []*ThreatPattern

func init() {
	raw := []struct {
		name     string
		pattern  string
		desc     string
		risk     RiskLevel
		category ThreatCategory
	}{
		// --- FileSystemDestruction (Critical) ---
		{
			"rm_rf_root", `rm\s+-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*\s+/\s`,
			"Recursive forced deletion of root filesystem", RiskCritical, CatFileSystemDestruction,
		},
		{
			"rm_rf_system", `rm\s+-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*\s+/(etc|usr|var|boot|sys|proc|dev|lib)`,
			"Recursive forced deletion of system directories", RiskCritical, CatFileSystemDestruction,
		},
		{
			"dd_destruction", `dd\s+.*\bof=/dev/(sd[a-z]|nvme|hd[a-z]|disk)`,
			"Direct disk write via dd to block device", RiskCritical, CatFileSystemDestruction,
		},
		{
			"format_drive", `mkfs\.\w+\s+/dev/`,
			"Formatting a disk partition", RiskCritical, CatFileSystemDestruction,
		},

		// --- RemoteCodeExecution (Critical) ---
		{
			"curl_bash", `curl\s+.*\|\s*(ba)?sh`,
			"Downloading and executing remote script via curl|bash", RiskCritical, CatRemoteCodeExecution,
		},
		{
			"wget_bash", `wget\s+.*\|\s*(ba)?sh`,
			"Downloading and executing remote script via wget|bash", RiskCritical, CatRemoteCodeExecution,
		},
		{
			"curl_bash_process_sub", `(ba)?sh\s+<\(curl`,
			"Executing remote code via process substitution", RiskCritical, CatRemoteCodeExecution,
		},
		{
			"python_remote_exec", `python[23]?\s+-c\s+.*urllib.*exec`,
			"Python remote code download and execution", RiskCritical, CatRemoteCodeExecution,
		},
		{
			"powershell_download_exec", `(?i)powershell.*downloadstring.*invoke-expression`,
			"PowerShell download and execute pattern", RiskCritical, CatRemoteCodeExecution,
		},

		// --- DataExfiltration (High) ---
		{
			"ssh_key_exfil_curl", `curl\s+.*-d\s+.*\.ssh/`,
			"Exfiltrating SSH keys via curl POST", RiskHigh, CatDataExfiltration,
		},
		{
			"ssh_key_exfil_wget", `wget\s+.*--post-file.*\.ssh/`,
			"Exfiltrating SSH keys via wget POST", RiskHigh, CatDataExfiltration,
		},
		{
			"password_file_exfil", `(curl|wget)\s+.*(/etc/passwd|/etc/shadow)`,
			"Exfiltrating system password files", RiskHigh, CatDataExfiltration,
		},
		{
			"history_exfil", `(curl|wget)\s+.*\.(bash_history|zsh_history)`,
			"Exfiltrating shell history files", RiskHigh, CatDataExfiltration,
		},
		{
			"env_exfil", `(curl|wget)\s+.*(/proc/self/environ|\.env\b)`,
			"Exfiltrating environment variables or .env files", RiskHigh, CatDataExfiltration,
		},

		// --- SystemModification (High) ---
		{
			"crontab_persist", `crontab\s+-[el]?\s*.*\|.*crontab\s+-`,
			"Modifying crontab for persistence", RiskHigh, CatSystemModification,
		},
		{
			"systemd_service", `cp\s+.*\.service\s+/etc/systemd/`,
			"Installing a systemd service for persistence", RiskHigh, CatSystemModification,
		},
		{
			"hosts_modification", `echo\s+.*>>\s*/etc/hosts`,
			"Modifying /etc/hosts file", RiskHigh, CatSystemModification,
		},

		// --- NetworkAccess (High/Critical) ---
		{
			"netcat_listener", `nc\s+-[a-zA-Z]*l[a-zA-Z]*p?\s+\d+`,
			"Opening a netcat listener (potential reverse shell)", RiskCritical, CatNetworkAccess,
		},
		{
			"reverse_shell_bash", `bash\s+-i\s+>&\s*/dev/tcp/`,
			"Bash reverse shell via /dev/tcp", RiskCritical, CatNetworkAccess,
		},
		{
			"reverse_shell_python", `python[23]?\s+-c\s+.*socket.*connect`,
			"Python reverse shell", RiskCritical, CatNetworkAccess,
		},
		{
			"ssh_tunnel", `ssh\s+.*-[RL]\s+\d+:`,
			"SSH port forwarding / tunneling", RiskHigh, CatNetworkAccess,
		},

		// --- PrivilegeEscalation (Critical/High) ---
		{
			"sudo_nopasswd", `echo\s+.*NOPASSWD.*>>\s*/etc/sudoers`,
			"Adding passwordless sudo access", RiskCritical, CatPrivilegeEscalation,
		},
		{
			"suid_binary", `chmod\s+[u+]*s\s+`,
			"Setting SUID bit on a binary", RiskHigh, CatPrivilegeEscalation,
		},
		{
			"docker_privileged", `docker\s+run\s+.*--privileged`,
			"Running Docker container in privileged mode", RiskHigh, CatPrivilegeEscalation,
		},
		{
			"kernel_module", `insmod\s+|modprobe\s+`,
			"Loading kernel modules", RiskHigh, CatPrivilegeEscalation,
		},

		// --- CommandInjection (High/Medium) ---
		{
			"base64_exec", `base64\s+-d.*\|\s*(ba)?sh`,
			"Decoding base64 and piping to shell execution", RiskHigh, CatCommandInjection,
		},
		{
			"hex_exec", `xxd\s+-r.*\|\s*(ba)?sh`,
			"Decoding hex and piping to shell execution", RiskHigh, CatCommandInjection,
		},
		{
			"eval_variable", `eval\s+\$`,
			"Eval with variable expansion (injection risk)", RiskMedium, CatCommandInjection,
		},
		{
			"python_eval", `python[23]?\s+-c\s+.*\beval\b`,
			"Python eval from command line", RiskMedium, CatCommandInjection,
		},
	}

	patterns = make([]*ThreatPattern, 0, len(raw))
	for _, r := range raw {
		compiled, err := regexp.Compile(r.pattern)
		if err != nil {
			// Programming error — panic during init is acceptable.
			panic("security: bad pattern " + r.name + ": " + err.Error())
		}
		patterns = append(patterns, &ThreatPattern{
			Name:        r.name,
			Compiled:    compiled,
			Description: r.desc,
			Risk:        r.risk,
			Category:    r.category,
		})
	}
}
