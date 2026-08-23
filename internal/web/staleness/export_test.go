package staleness

import (
	"context"

	"github.com/bruin-data/bruin/pkg/pipeline"
)

// TestCacheSizes exposes cache occupancy to the external contract tests without
// making scheduler test dependencies part of the staleness package itself.
func (s *Service) TestCacheSizes() (selections, snapshots int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.selections), len(s.snapshots)
}

func (s *Service) TestSetResolveTargets(resolve func(
	context.Context,
	Selection,
	*pipeline.Pipeline,
) (map[string]PhysicalTarget, error)) {
	s.deps.ResolveTargets = resolve
}

func (s *Service) TestSetVerify(verify func(
	context.Context,
	Selection,
	[]string,
) (map[string]bool, error)) {
	s.deps.Verify = verify
}

func VerifiableByNameForTest(asset *pipeline.Asset) bool {
	return verifiableByName(asset)
}
