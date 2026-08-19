package notebookmcp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"renart/internal/web/model"
	"renart/internal/web/service"
)

const (
	preparedChangeTTL        = 30 * time.Minute
	maxPreparedChanges       = 64
	maxMCPChangeOperations   = 50
	maxMCPChangePayloadBytes = 1 << 20
)

var supportedMCPChangeOperationKinds = []string{
	service.NotebookOperationManifestUpgrade,
	service.NotebookOperationCellCreate,
	service.NotebookOperationCellUpdate,
	service.NotebookOperationCellRename,
	service.NotebookOperationCellSourceConfigure,
	service.NotebookOperationSourceCreate,
	service.NotebookOperationMarkdownCreate,
	service.NotebookOperationMarkdownUpdate,
	service.NotebookOperationVisualizationCreate,
	service.NotebookOperationVisualizationUpdate,
	service.NotebookOperationVisualizationMigrate,
	service.NotebookOperationParametersReplace,
	service.NotebookOperationControlCreate,
	service.NotebookOperationControlUpdate,
	service.NotebookOperationControlDelete,
	service.NotebookOperationBlockMove,
}

type storedChange struct {
	id         string
	notebookID string
	plan       service.NotebookChangePlan
	createdAt  time.Time
	expiresAt  time.Time
	applying   bool
}

type changeStore struct {
	mu      sync.Mutex
	changes map[string]*storedChange
	now     func() time.Time
}

func newChangeStore() *changeStore {
	return &changeStore{changes: map[string]*storedChange{}, now: time.Now}
}

func (s *changeStore) put(notebookID string, plan service.NotebookChangePlan) (*storedChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	if len(s.changes) >= maxPreparedChanges {
		var oldest *storedChange
		for _, candidate := range s.changes {
			if oldest == nil || candidate.createdAt.Before(oldest.createdAt) {
				oldest = candidate
			}
		}
		if oldest != nil && !oldest.applying {
			delete(s.changes, oldest.id)
		}
	}
	if len(s.changes) >= maxPreparedChanges {
		return nil, fmt.Errorf("too many prepared notebook changes are active; discard one and try again")
	}
	id, err := randomOpaqueID("change")
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	change := &storedChange{
		id: id, notebookID: notebookID, plan: plan, createdAt: now,
		expiresAt: now.Add(preparedChangeTTL),
	}
	s.changes[id] = change
	return cloneStoredChange(change), nil
}

func (s *changeStore) get(id string) (*storedChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	change, ok := s.changes[id]
	if !ok {
		return nil, fmt.Errorf("prepared change %q was not found or has expired", id)
	}
	return cloneStoredChange(change), nil
}

func (s *changeStore) beginApply(id string) (*storedChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	change, ok := s.changes[id]
	if !ok {
		return nil, fmt.Errorf("prepared change %q was not found or has expired", id)
	}
	if change.applying {
		return nil, fmt.Errorf("prepared change %q is already being applied", id)
	}
	change.applying = true
	return cloneStoredChange(change), nil
}

func (s *changeStore) finishApply(id string, applied bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if applied {
		delete(s.changes, id)
		return
	}
	if change := s.changes[id]; change != nil {
		change.applying = false
	}
}

func (s *changeStore) discard(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	change, ok := s.changes[id]
	if !ok || change.applying {
		return false
	}
	delete(s.changes, id)
	return true
}

func (s *changeStore) sweepLocked() {
	now := s.now()
	for id, change := range s.changes {
		if !change.applying && !now.Before(change.expiresAt) {
			delete(s.changes, id)
		}
	}
}

func cloneStoredChange(change *storedChange) *storedChange {
	if change == nil {
		return nil
	}
	clone := *change
	clone.plan.ChangeSet.Operations = append([]service.NotebookOperation(nil), change.plan.ChangeSet.Operations...)
	clone.plan.Diff = append([]service.NotebookChangeDiff(nil), change.plan.Diff...)
	clone.plan.Problems = append([]string(nil), change.plan.Problems...)
	clone.plan.BlockingProblems = append([]string(nil), change.plan.BlockingProblems...)
	return &clone
}

func (s *Server) prepareChangeSet(ctx context.Context, _ *mcp.CallToolRequest, input PrepareChangeSetInput) (*mcp.CallToolResult, PreparedChangeOutput, error) {
	if strings.TrimSpace(input.NotebookID) == "" || strings.TrimSpace(input.BaseRevision) == "" {
		return nil, PreparedChangeOutput{}, fmt.Errorf("notebook_id and base_revision are required")
	}
	if _, err := s.loadNotebook(ctx, input.NotebookID); err != nil {
		return nil, PreparedChangeOutput{}, err
	}
	if err := validateMCPChangeOperations(input.Operations); err != nil {
		return nil, PreparedChangeOutput{}, err
	}
	plan, err := s.backend.PrepareChangeSet(ctx, input.NotebookID, service.NotebookChangeSet{
		BaseRevision: input.BaseRevision, Operations: input.Operations,
	})
	if err != nil {
		return nil, PreparedChangeOutput{}, err
	}
	stored, err := s.changes.put(input.NotebookID, plan)
	if err != nil {
		return nil, PreparedChangeOutput{}, err
	}
	return nil, preparedOutput(stored), nil
}

func (s *Server) validateChangeSet(ctx context.Context, _ *mcp.CallToolRequest, input PreparedChangeInput) (*mcp.CallToolResult, PreparedChangeOutput, error) {
	stored, err := s.changes.get(strings.TrimSpace(input.PreparedID))
	if err != nil {
		return nil, PreparedChangeOutput{}, err
	}
	plan, err := s.backend.PrepareChangeSet(ctx, stored.notebookID, stored.plan.ChangeSet)
	if err != nil {
		return nil, PreparedChangeOutput{}, err
	}
	if plan.ChangeSet.ExpectedRevision != stored.plan.ChangeSet.ExpectedRevision {
		return nil, PreparedChangeOutput{}, fmt.Errorf("prepared notebook change no longer normalizes to the reviewed revision; prepare it again")
	}
	validated := *stored
	validated.plan = plan
	return nil, preparedOutput(&validated), nil
}

func (s *Server) applyChangeSet(ctx context.Context, _ *mcp.CallToolRequest, input PreparedChangeInput) (*mcp.CallToolResult, ApplyChangeOutput, error) {
	preparedID := strings.TrimSpace(input.PreparedID)
	stored, err := s.changes.beginApply(preparedID)
	if err != nil {
		return nil, ApplyChangeOutput{}, err
	}
	applied := false
	defer func() { s.changes.finishApply(preparedID, applied) }()
	if !stored.plan.CanApply {
		return nil, ApplyChangeOutput{}, fmt.Errorf("prepared change has blocking problems: %s", strings.Join(stored.plan.BlockingProblems, "; "))
	}
	result, err := s.backend.ApplyChangeSet(ctx, stored.notebookID, stored.plan.ChangeSet)
	if err != nil {
		return nil, ApplyChangeOutput{}, err
	}
	applied = true
	return nil, ApplyChangeOutput{
		SchemaVersion: SchemaVersion, Notebook: summarizeNotebook(result.Notebook), Applied: true,
	}, nil
}

func (s *Server) discardChangeSet(_ context.Context, _ *mcp.CallToolRequest, input PreparedChangeInput) (*mcp.CallToolResult, DiscardChangeOutput, error) {
	id := strings.TrimSpace(input.PreparedID)
	if id == "" {
		return nil, DiscardChangeOutput{}, fmt.Errorf("prepared_id is required")
	}
	discarded := s.changes.discard(id)
	return nil, DiscardChangeOutput{SchemaVersion: SchemaVersion, PreparedID: id, Discarded: discarded}, nil
}

func validateMCPChangeOperations(operations []service.NotebookOperation) error {
	if len(operations) == 0 {
		return fmt.Errorf("at least one notebook operation is required")
	}
	if len(operations) > maxMCPChangeOperations {
		return fmt.Errorf("an MCP notebook change may contain at most %d operations", maxMCPChangeOperations)
	}
	for _, operation := range operations {
		if !slices.Contains(supportedMCPChangeOperationKinds, operation.Kind) {
			return fmt.Errorf(
				"operation kind %q is not available through MCP; use one of: %s",
				operation.Kind,
				strings.Join(supportedMCPChangeOperationKinds, ", "),
			)
		}
		if len(operation.Content) > maxBlockContentBytes {
			return fmt.Errorf("operation %q content exceeds the MCP limit of %d bytes", operation.Kind, maxBlockContentBytes)
		}
	}
	encoded, err := json.Marshal(operations)
	if err != nil {
		return fmt.Errorf("encode notebook operations: %w", err)
	}
	if len(encoded) > maxMCPChangePayloadBytes {
		return fmt.Errorf("notebook change exceeds the MCP payload limit of %d bytes", maxMCPChangePayloadBytes)
	}
	return nil
}

func preparedOutput(stored *storedChange) PreparedChangeOutput {
	operations := make([]PreparedOperation, 0, len(stored.plan.ChangeSet.Operations))
	for _, operation := range stored.plan.ChangeSet.Operations {
		operations = append(operations, safePreparedOperation(operation))
	}
	diff := make([]PreparedDiff, 0, len(stored.plan.Diff))
	statuses := make([]string, 0, len(stored.plan.Diff))
	for _, change := range stored.plan.Diff {
		statuses = append(statuses, change.Status)
	}
	sort.Strings(statuses)
	for index, status := range statuses {
		diff = append(diff, PreparedDiff{Status: status, Subject: fmt.Sprintf("authored notebook file %d", index+1)})
	}
	return PreparedChangeOutput{
		SchemaVersion: SchemaVersion, PreparedID: stored.id, NotebookID: stored.notebookID,
		BaseRevision:     stored.plan.ChangeSet.BaseRevision,
		ExpectedRevision: stored.plan.ChangeSet.ExpectedRevision,
		Operations:       operations, Diff: diff,
		Problems:         append([]string(nil), stored.plan.Problems...),
		BlockingProblems: append([]string(nil), stored.plan.BlockingProblems...),
		CanApply:         stored.plan.CanApply, ExpiresAt: stored.expiresAt.Format(time.RFC3339),
	}
}

func safePreparedOperation(operation service.NotebookOperation) PreparedOperation {
	result := PreparedOperation{
		Kind: operation.Kind, CellID: operation.CellID, BlockID: operation.BlockID,
		ControlID: operation.ControlID,
		Name:      operation.Name, Language: operation.Language, Connection: operation.Connection,
		AssetType: operation.AssetType, SnapshotMode: operation.SnapshotMode,
		RowLimit: operation.RowLimit, Content: operation.Content,
		Visualization: cloneVisualization(operation.Visualization),
		Parameter:     cloneNotebookParameter(operation.Parameter),
		Parameters:    cloneNotebookParameterDTOs(operation.Parameters),
		Position:      operation.Position, AfterBlockID: operation.AfterBlockID,
	}
	if operation.Source != nil {
		safe := safeSourceDefinition(*operation.Source)
		result.Source = &safe
	}
	return result
}

func cloneNotebookParameter(value *model.NotebookParameter) *model.NotebookParameter {
	if value == nil {
		return nil
	}
	cloned := cloneNotebookParameterDTOs([]model.NotebookParameter{*value})
	return &cloned[0]
}

func cloneVisualization(value *model.NotebookVisualization) *model.NotebookVisualization {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		clone := *value
		return &clone
	}
	var clone model.NotebookVisualization
	if err := json.Unmarshal(encoded, &clone); err != nil {
		fallback := *value
		return &fallback
	}
	return &clone
}

func randomOpaqueID(prefix string) (string, error) {
	var data [18]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate opaque ID: %w", err)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(data[:]), nil
}
