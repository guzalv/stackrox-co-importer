package features

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
	"github.com/cucumber/godog/colors"
)

// TestFeatures runs all Gherkin .feature files in specs/ via Godog.
// Step definitions are registered in InitializeScenario.
// Undefined steps are reported as pending — the starting state for new specs.
func TestFeatures(t *testing.T) {
	opts := godog.Options{
		Format:      "pretty",
		Paths:       []string{"../specs"},
		Output:      colors.Colored(os.Stdout),
		Concurrency: 0, // sequential for now
		TestingT:    t,
		Strict:      false, // false = undefined steps are pending, not failures
	}

	suite := godog.TestSuite{
		Name:                "co-importer",
		ScenarioInitializer: InitializeScenario,
		Options:             &opts,
	}

	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
