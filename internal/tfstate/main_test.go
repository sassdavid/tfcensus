package tfstate_test

import (
	"testing"

	"github.com/sassdavid/tfcensus/internal/testutil/fakebin"
)

// TestMain lets the test binary act as the fake tofu and terragrunt of tofu_test.go.
func TestMain(m *testing.M) { fakebin.Main(m) }
