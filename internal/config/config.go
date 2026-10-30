// Package config parses and validates CLI flags and environment variables
// for the CO-to-ACS importer.
package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	flag "github.com/spf13/pflag"
)

// Auth mode constants.
const (
	AuthModeToken = "token"
	AuthModeBasic = "basic"
)

// Exit code constants.
const (
	// IMP-CLI-017: run completed with no failed bindings.
	ExitOK = 0
	// IMP-CLI-018: fatal preflight/config errors (no import attempted).
	ExitConfigError = 1
	// IMP-CLI-019: partial success (some bindings failed).
	ExitPartialSuccess = 2
)

// ErrHelpRequested is returned by Parse when --help/-h is passed. pflag has
// already printed the usage text, so the caller should exit 0 without printing
// an error. IMP-CLI-035
var ErrHelpRequested = errors.New("help requested")

const (
	defaultTimeout       = 30 * time.Second
	defaultMaxRetries    = 5
	defaultCONamespace   = "openshift-compliance"
	defaultUsername      = "admin"
	defaultSettleTimeout = 5 * time.Second
)

// Config holds all resolved configuration for a run.
type Config struct {
	Endpoint           string
	AuthMode           string // "token" or "basic"
	Token              string
	Username           string
	Password           string
	Namespace          string
	AllNamespaces      bool
	DryRun             bool
	OverwriteExisting  bool
	RequestTimeout     time.Duration
	MaxRetries         int
	CACertFile         string
	InsecureSkipVerify bool
	Contexts           []string
	ReportJSON         string
	// IMP-CLI-028: exclude patterns (validated Go regexes).
	ExcludePatterns []string
	// IMP-CLI-029: list SSBs and exit without importing.
	ListSSBs bool
	// IMP-CLI-030: delete blocking external ScanSettingBindings instead of skipping.
	DeleteExisting bool
	// IMP-CLI-031: skip the interactive confirmation prompt for --delete-existing.
	ConfirmYes bool
	// IMP-CLI-032: directory for backups of deleted resources and the resumable plan.
	BackupDir string
	// IMP-CLI-033: re-poll duration after a blocking external SSB is observed
	// gone, to detect GitOps self-healing before creating the scan config.
	SettleTimeout time.Duration
	// Warnings collects non-fatal configuration notices (e.g. overlapping auth vars).
	Warnings []string
}

// envFunc is the type for a function that looks up environment variables.
// Used to decouple from os.Getenv for testability.
type envFunc func(string) string

// ttyFunc is the type for a function that reports whether stdin is an
// interactive terminal. Used to decouple from a real TTY check for
// testability (IMP-CLI-034).
type ttyFunc func() bool

// Parse creates a Config from command-line args and environment variables.
// The env parameter is a lookup function (typically os.Getenv) so tests can
// supply their own values without mutating process state. The stdinIsTTY
// parameter reports whether stdin is an interactive terminal (IMP-CLI-034).
func Parse(args []string, getenv envFunc, stdinIsTTY ttyFunc) (*Config, error) {
	fs := flag.NewFlagSet("co-importer", flag.ContinueOnError)

	// IMP-CLI-001: endpoint from flag or env var
	endpointDefault := getenv("ROX_ENDPOINT")
	endpoint := fs.String("endpoint", endpointDefault,
		"ACS Central endpoint (or set ROX_ENDPOINT)")

	// IMP-CLI-004: namespace scope
	coNamespace := fs.String("co-namespace", defaultCONamespace,
		"Compliance Operator namespace")
	coAllNamespaces := fs.Bool("co-all-namespaces", false,
		"Read CO resources from all namespaces")

	// IMP-CLI-006, IMP-CLI-027: overwrite-existing
	overwriteExisting := fs.Bool("overwrite-existing", false,
		"Update existing ACS scan configs instead of skipping")

	// IMP-CLI-007: dry-run
	dryRun := fs.Bool("dry-run", false,
		"Preview actions without writing to ACS")

	// IMP-CLI-009: request timeout
	requestTimeout := fs.Duration("request-timeout", defaultTimeout,
		"Timeout per HTTP request (e.g. 30s, 1m)")

	// IMP-CLI-010: max retries
	maxRetries := fs.Int("max-retries", defaultMaxRetries,
		"Max retry attempts for transient failures")

	// IMP-CLI-011: CA cert file
	caCertFile := fs.String("ca-cert-file", "",
		"Path to PEM CA certificate bundle")

	// IMP-CLI-012: insecure skip verify
	insecureSkipVerify := fs.Bool("insecure-skip-verify", false,
		"Skip TLS certificate verification")

	// IMP-CLI-024: username for basic auth
	username := fs.String("username", "",
		"Username for basic auth (default \"admin\", or ROX_ADMIN_USER)")

	// IMP-CLI-008: report-json
	reportJSON := fs.String("report-json", "",
		"Write JSON report to this path")

	// IMP-CLI-003: context filter
	contexts := fs.StringArray("context", nil,
		"Kubernetes context to process (repeatable)")

	// IMP-CLI-028: SSB exclusion patterns
	excludePatterns := fs.StringArray("exclude", nil,
		"Exclude SSBs whose names match this Go regex (repeatable)")

	// IMP-CLI-029: list-ssbs mode
	listSSBs := fs.Bool("list-ssbs", false,
		"List discovered SSBs and exit (no import)")

	// IMP-CLI-030: delete-existing mode
	deleteExisting := fs.Bool("delete-existing", false,
		"Delete blocking external ScanSettingBindings after confirmation")

	// IMP-CLI-031: confirm-yes, skips the interactive confirmation prompt
	confirmYes := fs.BoolP("yes", "y", false,
		"Skip the interactive confirmation prompt (used with --delete-existing)")

	// IMP-CLI-032: backup-dir. Default is empty here (sentinel); when the
	// flag is not explicitly set, Parse fills in a timestamped default below.
	backupDir := fs.String("backup-dir", "",
		"Directory for resource backups and the resumable plan "+
			"(default \"./co-importer-backup-<timestamp>\")")

	// IMP-CLI-033: settle timeout
	settleTimeout := fs.Duration("settle-timeout", defaultSettleTimeout,
		"Re-poll duration after a blocking external SSB is observed gone, "+
			"to detect GitOps self-healing before creating the scan config")

	if err := fs.Parse(args); err != nil {
		// IMP-CLI-035: --help/-h is a successful invocation, not an error.
		// pflag already printed the usage text; signal a clean exit.
		if errors.Is(err, flag.ErrHelp) {
			return nil, ErrHelpRequested
		}
		return nil, fmt.Errorf("flag parse error: %w", err)
	}

	// Resolve username: flag > env > default (IMP-CLI-024)
	resolvedUsername := *username
	if resolvedUsername == "" {
		resolvedUsername = getenv("ROX_ADMIN_USER")
	}
	if resolvedUsername == "" {
		resolvedUsername = defaultUsername
	}

	cfg := &Config{
		Endpoint:           *endpoint,
		Username:           resolvedUsername,
		Namespace:          *coNamespace,
		AllNamespaces:      *coAllNamespaces,
		DryRun:             *dryRun,
		OverwriteExisting:  *overwriteExisting,
		RequestTimeout:     *requestTimeout,
		MaxRetries:         *maxRetries,
		CACertFile:         *caCertFile,
		InsecureSkipVerify: *insecureSkipVerify,
		Contexts:           *contexts,
		ReportJSON:         *reportJSON,
		ExcludePatterns:    *excludePatterns,
		ListSSBs:           *listSSBs,
		DeleteExisting:     *deleteExisting,
		ConfirmYes:         *confirmYes,
		BackupDir:          *backupDir,
		SettleTimeout:      *settleTimeout,
	}

	// IMP-CLI-032: default to a timestamped directory when --backup-dir is not given.
	if cfg.BackupDir == "" {
		cfg.BackupDir = defaultBackupDir()
	}

	// IMP-CLI-029: --list-ssbs does not contact ACS; skip auth validation entirely.
	if !cfg.ListSSBs {
		// IMP-CLI-002: auto-infer auth mode
		if err := inferAuthMode(cfg, getenv); err != nil {
			return nil, err
		}
	}

	// Validate cross-field invariants
	if err := validate(cfg, stdinIsTTY); err != nil {
		return nil, err
	}

	return cfg, nil
}

// IMP-CLI-032: defaultBackupDir returns a timestamped backup directory path,
// relative to the current working directory. The timestamp uses a
// filesystem-safe variant of RFC3339 (colons replaced with dashes, no
// punctuation separators) so the path works unmodified on all platforms.
func defaultBackupDir() string {
	return "co-importer-backup-" + time.Now().UTC().Format("20060102T150405Z")
}

// inferAuthMode sets cfg.AuthMode based on which env vars are present.
// IMP-CLI-002, IMP-CLI-025
func inferAuthMode(cfg *Config, getenv envFunc) error {
	token := getenv("ROX_API_TOKEN")
	password := getenv("ROX_ADMIN_PASSWORD")

	hasToken := token != ""
	hasPassword := password != ""

	switch {
	case hasToken && hasPassword:
		// IMP-CLI-025: token takes precedence; emit a warning
		cfg.Warnings = append(cfg.Warnings,
			"both ROX_API_TOKEN and ROX_ADMIN_PASSWORD are set; using token auth",
		)
		cfg.AuthMode = AuthModeToken
		cfg.Token = token
	case hasToken:
		cfg.AuthMode = AuthModeToken
		cfg.Token = token
	case hasPassword:
		cfg.AuthMode = AuthModeBasic
		cfg.Password = password
	default:
		// IMP-CLI-025: neither set
		return errors.New(
			"no auth credentials found; set ROX_API_TOKEN for token auth, or ROX_ADMIN_PASSWORD for basic auth",
		)
	}
	return nil
}

// validate checks cross-field invariants after flags and env vars are resolved.
func validate(cfg *Config, stdinIsTTY ttyFunc) error {
	// IMP-CLI-029: --list-ssbs never contacts ACS, so no endpoint is
	// required and the endpoint/scheme checks below do not apply.
	if !cfg.ListSSBs {
		// IMP-CLI-001: endpoint required
		if cfg.Endpoint == "" {
			return errors.New("--endpoint is required (or set ROX_ENDPOINT)")
		}

		// IMP-CLI-013: scheme handling
		if strings.HasPrefix(cfg.Endpoint, "http://") {
			return fmt.Errorf(
				"--endpoint must use HTTPS (got %q); use https:// or omit the scheme",
				cfg.Endpoint,
			)
		}
		if !strings.HasPrefix(cfg.Endpoint, "https://") {
			cfg.Endpoint = "https://" + cfg.Endpoint
		}

		// Strip trailing slash for consistency.
		cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	}

	// IMP-CLI-014: auth material must be non-empty
	switch cfg.AuthMode {
	case AuthModeToken:
		if cfg.Token == "" {
			return errors.New("ROX_API_TOKEN is set but empty")
		}
	case AuthModeBasic:
		if cfg.Password == "" {
			return errors.New("ROX_ADMIN_PASSWORD is set but empty")
		}
	}

	// IMP-CLI-004: all-namespaces overrides namespace
	if cfg.AllNamespaces {
		cfg.Namespace = ""
	}

	// IMP-CLI-010: max-retries minimum
	if cfg.MaxRetries < 0 {
		return fmt.Errorf("--max-retries must be >= 0 (got %d)", cfg.MaxRetries)
	}

	// IMP-CLI-028: validate exclude patterns are valid Go regexes
	for _, p := range cfg.ExcludePatterns {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("invalid --exclude regex %q: %w", p, err)
		}
	}

	// IMP-CLI-034: --delete-existing without --yes needs an interactive
	// terminal to show the confirmation prompt. Fail fast when stdin is not
	// a TTY, since the prompt cannot be shown non-interactively.
	if cfg.DeleteExisting && !cfg.ConfirmYes && !stdinIsTTY() {
		return errors.New(
			"--delete-existing requires --yes when stdin is not a TTY " +
				"(no interactive terminal available to confirm the deletion)",
		)
	}

	return nil
}
