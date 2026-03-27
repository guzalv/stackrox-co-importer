package features

import (
	"github.com/cucumber/godog"
)

// InitializeScenario registers all step definitions for all feature files.
// This is the single entry point called by TestFeatures for each scenario.
func InitializeScenario(ctx *godog.ScenarioContext) {
	registerMappingSteps(ctx)
	registerIdempotencySteps(ctx)
}
