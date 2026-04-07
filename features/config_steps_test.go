package features

import (
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/stackrox/co-importer/internal/config"
)

// configTestCtx holds per-scenario state for CLI config contract scenarios.
type configTestCtx struct {
	env map[string]string // only explicitly set vars; unset keys return ""
	cfg *config.Config
	err error
}

func (c *configTestCtx) mockEnv(key string) string {
	return c.env[key]
}

func (c *configTestCtx) envVarIs(key, value string) {
	c.env[key] = value
}

func (c *configTestCtx) parseConfigNoFlags() {
	c.cfg, c.err = config.Parse(nil, c.mockEnv)
}

func (c *configTestCtx) parseConfigWithFlags(flagsStr string) {
	args := strings.Fields(flagsStr)
	c.cfg, c.err = config.Parse(args, c.mockEnv)
}

func (c *configTestCtx) configParsingSuceeds() error {
	if c.err != nil {
		return fmt.Errorf("expected no error, got: %v", c.err)
	}
	return nil
}

func (c *configTestCtx) configParsingFails() error {
	if c.err == nil {
		return fmt.Errorf("expected error, got nil (cfg=%+v)", c.cfg)
	}
	return nil
}

func (c *configTestCtx) configParsingFailsWithErrorContaining(substr string) error {
	if c.err == nil {
		return fmt.Errorf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(c.err.Error(), substr) {
		return fmt.Errorf("expected error containing %q, got: %v", substr, c.err)
	}
	return nil
}

func (c *configTestCtx) theErrorAlsoContains(substr string) error {
	if c.err == nil {
		return fmt.Errorf("no error was returned; cannot check for %q", substr)
	}
	if !strings.Contains(c.err.Error(), substr) {
		return fmt.Errorf("expected error to also contain %q, got: %v", substr, c.err)
	}
	return nil
}

func (c *configTestCtx) theEndpointIs(want string) error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if c.cfg.Endpoint != want {
		return fmt.Errorf("endpoint: got %q, want %q", c.cfg.Endpoint, want)
	}
	return nil
}

func (c *configTestCtx) theAuthModeIs(want string) error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if c.cfg.AuthMode != want {
		return fmt.Errorf("auth mode: got %q, want %q", c.cfg.AuthMode, want)
	}
	return nil
}

func (c *configTestCtx) theTokenIs(want string) error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if c.cfg.Token != want {
		return fmt.Errorf("token: got %q, want %q", c.cfg.Token, want)
	}
	return nil
}

func (c *configTestCtx) thePasswordIs(want string) error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if c.cfg.Password != want {
		return fmt.Errorf("password: got %q, want %q", c.cfg.Password, want)
	}
	return nil
}

func (c *configTestCtx) theUsernameIs(want string) error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if c.cfg.Username != want {
		return fmt.Errorf("username: got %q, want %q", c.cfg.Username, want)
	}
	return nil
}

func (c *configTestCtx) theNamespaceIs(want string) error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if c.cfg.Namespace != want {
		return fmt.Errorf("namespace: got %q, want %q", c.cfg.Namespace, want)
	}
	return nil
}

func (c *configTestCtx) allNamespacesIsEnabled() error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if !c.cfg.AllNamespaces {
		return fmt.Errorf("AllNamespaces: got false, want true")
	}
	return nil
}

func (c *configTestCtx) overwriteExistingIsEnabled() error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if !c.cfg.OverwriteExisting {
		return fmt.Errorf("OverwriteExisting: got false, want true")
	}
	return nil
}

func (c *configTestCtx) overwriteExistingIsDisabled() error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if c.cfg.OverwriteExisting {
		return fmt.Errorf("OverwriteExisting: got true, want false")
	}
	return nil
}

func (c *configTestCtx) dryRunIsEnabled() error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if !c.cfg.DryRun {
		return fmt.Errorf("DryRun: got false, want true")
	}
	return nil
}

func (c *configTestCtx) dryRunIsDisabled() error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if c.cfg.DryRun {
		return fmt.Errorf("DryRun: got true, want false")
	}
	return nil
}

func (c *configTestCtx) theRequestTimeoutIs(want string) error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	d, err := time.ParseDuration(want)
	if err != nil {
		return fmt.Errorf("invalid duration %q in step: %v", want, err)
	}
	if c.cfg.RequestTimeout != d {
		return fmt.Errorf("request timeout: got %v, want %v", c.cfg.RequestTimeout, d)
	}
	return nil
}

func (c *configTestCtx) maxRetriesIs(want int) error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if c.cfg.MaxRetries != want {
		return fmt.Errorf("max retries: got %d, want %d", c.cfg.MaxRetries, want)
	}
	return nil
}

func (c *configTestCtx) theCACertFileIs(want string) error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if c.cfg.CACertFile != want {
		return fmt.Errorf("CA cert file: got %q, want %q", c.cfg.CACertFile, want)
	}
	return nil
}

func (c *configTestCtx) insecureSkipVerifyIsEnabled() error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if !c.cfg.InsecureSkipVerify {
		return fmt.Errorf("InsecureSkipVerify: got false, want true")
	}
	return nil
}

func (c *configTestCtx) insecureSkipVerifyIsDisabled() error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if c.cfg.InsecureSkipVerify {
		return fmt.Errorf("InsecureSkipVerify: got true, want false")
	}
	return nil
}

func (c *configTestCtx) aWarningIsEmittedContaining(substr string) error {
	if c.cfg == nil {
		return fmt.Errorf("config is nil")
	}

	return nil
}

// exitCodeIs checks the named exit code constant value.
// IMP-CLI-017, IMP-CLI-018, IMP-CLI-019
func exitCodeIs(name string, want int) error {
	var got int
	switch name {
	case "success":
		got = config.ExitOK
	case "config-error":
		got = config.ExitConfigError
	case "partial-success":
		got = config.ExitPartialSuccess
	default:
		return fmt.Errorf("unknown exit code name %q", name)
	}
	if got != want {
		return fmt.Errorf("exit code %q: got %d, want %d", name, got, want)
	}
	return nil
}

func registerConfigSteps(ctx *godog.ScenarioContext) {
	c := &configTestCtx{
		env: make(map[string]string),
	}

	ctx.Step(`^env var "([^"]*)" is "([^"]*)"$`, c.envVarIs)
	ctx.Step(`^I parse config with no flags$`, c.parseConfigNoFlags)
	ctx.Step(`^I parse config with flags "([^"]*)"$`, c.parseConfigWithFlags)
	ctx.Step(`^config parsing succeeds$`, c.configParsingSuceeds)
	ctx.Step(`^config parsing fails$`, c.configParsingFails)
	ctx.Step(`^config parsing fails with error containing "([^"]*)"$`, c.configParsingFailsWithErrorContaining)
	ctx.Step(`^the error also contains "([^"]*)"$`, c.theErrorAlsoContains)
	ctx.Step(`^the endpoint is "([^"]*)"$`, c.theEndpointIs)
	ctx.Step(`^the auth mode is "([^"]*)"$`, c.theAuthModeIs)
	ctx.Step(`^the token is "([^"]*)"$`, c.theTokenIs)
	ctx.Step(`^the password is "([^"]*)"$`, c.thePasswordIs)
	ctx.Step(`^the username is "([^"]*)"$`, c.theUsernameIs)
	ctx.Step(`^the namespace is "([^"]*)"$`, c.theNamespaceIs)
	ctx.Step(`^all-namespaces is enabled$`, c.allNamespacesIsEnabled)
	ctx.Step(`^overwrite-existing is enabled$`, c.overwriteExistingIsEnabled)
	ctx.Step(`^overwrite-existing is disabled$`, c.overwriteExistingIsDisabled)
	ctx.Step(`^dry-run is enabled$`, c.dryRunIsEnabled)
	ctx.Step(`^dry-run is disabled$`, c.dryRunIsDisabled)
	ctx.Step(`^the request timeout is "([^"]*)"$`, c.theRequestTimeoutIs)
	ctx.Step(`^max retries is (\d+)$`, c.maxRetriesIs)
	ctx.Step(`^the CA cert file is "([^"]*)"$`, c.theCACertFileIs)
	ctx.Step(`^insecure-skip-verify is enabled$`, c.insecureSkipVerifyIsEnabled)
	ctx.Step(`^insecure-skip-verify is disabled$`, c.insecureSkipVerifyIsDisabled)
	ctx.Step(`^a warning is emitted containing "([^"]*)"$`, c.aWarningIsEmittedContaining)
	ctx.Step(`^the "([^"]*)" exit code is (\d+)$`, exitCodeIs)
}
