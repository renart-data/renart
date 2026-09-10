package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/execution"
	"renart/internal/web/policy"
	"renart/internal/web/scheduler"
)

func TestConnectionAccessContractSurvivesSchedulerRoundTrip(t *testing.T) {
	t.Parallel()
	contract := execution.ExecutionContract{
		AssetID: "asset", AssetName: "orders",
		AccessPolicyIdentity:  strings.Repeat("a", 64),
		AccessRequirements:    []execution.AccessRequirement{{ConnectionKey: strings.Repeat("b", 64), Effect: policy.Read, Operation: "sql"}},
		ConnectionKeys:        []string{strings.Repeat("b", 64)},
		MutationResources:     execution.Resources{Isolation: "resources"},
		CoordinationResources: execution.Resources{Isolation: "resources"},
	}
	durable := schedulerExecutionContractFromService(contract)
	require.True(t, execution.EqualExecutionContracts([]execution.ExecutionContract{contract}, []execution.ExecutionContract{durable}))
	recovered := serviceExecutionContractsFromScheduler([]scheduler.PipelineRunExecutionContract{durable})
	require.True(t, execution.EqualExecutionContracts([]execution.ExecutionContract{contract}, recovered))
	durable.AccessRequirements[0].Effect = policy.Write
	recovered[0].ConnectionKeys[0] = "changed"
	require.Equal(t, policy.Read, contract.AccessRequirements[0].Effect)
	require.Equal(t, strings.Repeat("b", 64), contract.ConnectionKeys[0])
}
