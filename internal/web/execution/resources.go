package execution

import (
	"sort"
	"strings"
)

// PipelineExclusiveResources returns the conservative admission contract used
// whenever an operator cannot prove a stable write-resource identity.
func PipelineExclusiveResources() Resources {
	return Resources{Isolation: PlanResourceIsolationPipeline, Claims: []ResourceClaim{}}
}

func CloneResources(resources Resources) Resources {
	return Resources{
		Isolation: resources.Isolation,
		Claims:    append([]ResourceClaim(nil), resources.Claims...),
	}
}

// CanonicalResources trims, sorts, and deduplicates resource claims so plan
// identity and scheduler arbitration consume the same deterministic value.
func CanonicalResources(resources Resources) Resources {
	result := Resources{
		Isolation: strings.TrimSpace(resources.Isolation),
		Claims:    append([]ResourceClaim(nil), resources.Claims...),
	}
	for index := range result.Claims {
		result.Claims[index].Kind = strings.TrimSpace(result.Claims[index].Kind)
		result.Claims[index].Identity = strings.TrimSpace(result.Claims[index].Identity)
	}
	sort.Slice(result.Claims, func(i, j int) bool {
		if result.Claims[i].Kind == result.Claims[j].Kind {
			return result.Claims[i].Identity < result.Claims[j].Identity
		}
		return result.Claims[i].Kind < result.Claims[j].Kind
	})
	deduped := result.Claims[:0]
	for _, claim := range result.Claims {
		if len(deduped) > 0 && deduped[len(deduped)-1] == claim {
			continue
		}
		deduped = append(deduped, claim)
	}
	result.Claims = deduped
	if result.Claims == nil {
		result.Claims = []ResourceClaim{}
	}
	return result
}

func AggregateMutationResources(contracts []ExecutionContract) Resources {
	result := Resources{Isolation: PlanResourceIsolationResources, Claims: []ResourceClaim{}}
	for _, contract := range contracts {
		if contract.MutationResources.Isolation == PlanResourceIsolationPipeline {
			result.Isolation = PlanResourceIsolationPipeline
		}
		result.Claims = append(result.Claims, contract.MutationResources.Claims...)
	}
	return CanonicalResources(result)
}

func EqualExecutionContracts(left, right []ExecutionContract) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].AssetID != right[index].AssetID ||
			left[index].AssetName != right[index].AssetName ||
			!equalStrings(left[index].ConnectionKeys, right[index].ConnectionKeys) ||
			!EqualResources(left[index].MutationResources, right[index].MutationResources) ||
			!EqualResources(left[index].CoordinationResources, right[index].CoordinationResources) {
			return false
		}
	}
	return true
}

func EqualResources(left, right Resources) bool {
	if left.Isolation != right.Isolation || len(left.Claims) != len(right.Claims) {
		return false
	}
	for index := range left.Claims {
		if left.Claims[index] != right.Claims[index] {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
