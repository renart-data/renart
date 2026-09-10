package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	webexecution "renart/internal/web/execution"
	"renart/internal/web/snapshot"
)

func TestSemanticImpactFindsSQLColumnAddedBeforeDeclarationsAreUpdated(t *testing.T) {
	files := semanticImpactPipelineFiles("INTEGER")
	candidateRoot := t.TempDir()
	for path, content := range files {
		if path == "assets/lineitems.sql" {
			content = strings.Replace(content, "AS total_amount", "AS total_amount, 'a@example.org' AS email", 1)
		}
		writeSemanticImpactFile(t, candidateRoot, path, content)
	}
	service := &PipelinePlanService{deps: PipelinePlanDependencies{Snapshots: &semanticImpactSnapshotStore{
		latest: &snapshot.Snapshot{VersionID: "baseline", PipelineUUID: "pipeline"}, files: files,
	}}}
	report := service.semanticImpactForDeployment(context.Background(), "pipeline", candidateRoot)
	for _, asset := range report.Assets {
		if asset.Name != "analytics.lineitems" {
			continue
		}
		if len(asset.Columns) != 1 || asset.Columns[0].Before != nil || asset.Columns[0].After.Name != "email" {
			t.Fatalf("new SQL output hidden by old declaration: %#v", asset)
		}
		if asset.AfterSource == nil || len(asset.AfterSource.Projections) != 2 {
			t.Fatalf("new output needs a source-backed projection: %#v", asset.AfterSource)
		}
		return
	}
	t.Fatalf("missing changed asset: %#v", report)
}

func TestPipelinePlanningSessionSemanticImpactUsesLatestDeploymentAsBaseline(t *testing.T) {
	candidateRoot := t.TempDir()
	for path, content := range semanticImpactPipelineFiles("DOUBLE") {
		writeSemanticImpactFile(t, candidateRoot, path, content)
	}
	store := &semanticImpactSnapshotStore{
		latest: &snapshot.Snapshot{VersionID: "snapshot-7", PipelineUUID: "pipeline-uuid"},
		files:  semanticImpactPipelineFiles("INTEGER"),
	}
	session := &pipelinePlanningSession{
		owner:  &PipelinePlanService{deps: PipelinePlanDependencies{Snapshots: store}},
		source: &resolvedPipelinePlanSource{pipelineDir: candidateRoot},
		input: webexecution.PlannerSessionInput{
			Plan: &webexecution.Plan{PipelineUUID: "pipeline-uuid"},
		},
	}

	report := session.SemanticImpact(context.Background())
	if report.Status != webexecution.SemanticImpactStatusAvailable || report.BaselineVersionID != "snapshot-7" {
		t.Fatalf("report baseline = %#v", report)
	}
	var revenue *webexecution.SemanticAssetImpact
	for index := range report.Assets {
		if report.Assets[index].Name == "analytics.revenue" {
			revenue = &report.Assets[index]
			break
		}
	}
	if revenue == nil {
		t.Fatalf("missing downstream impact: %#v", report.Assets)
	}
	if revenue.SourceChange != webexecution.SemanticSourceUnchanged || revenue.Origin != webexecution.SemanticImpactPropagated {
		t.Fatalf("downstream classification = %#v", revenue)
	}
	if len(revenue.Columns) != 1 || !revenue.Columns[0].TypeChanged {
		t.Fatalf("downstream columns = %#v", revenue.Columns)
	}
	if revenue.BeforeSource == nil || revenue.AfterSource == nil || len(revenue.AfterSource.Projections) != 1 {
		t.Fatalf("missing source-backed projection anchors: %#v", revenue)
	}
	if revenue.BeforeSource.Fingerprint != revenue.AfterSource.Fingerprint {
		t.Fatal("unchanged SQL should retain the same annotation identity")
	}
}

func TestPipelinePlanningSessionSemanticImpactHandlesFirstDeployment(t *testing.T) {
	session := &pipelinePlanningSession{
		owner:  &PipelinePlanService{deps: PipelinePlanDependencies{Snapshots: &semanticImpactSnapshotStore{}}},
		source: &resolvedPipelinePlanSource{pipelineDir: t.TempDir()},
		input:  webexecution.PlannerSessionInput{Plan: &webexecution.Plan{PipelineUUID: "pipeline-uuid"}},
	}
	report := session.SemanticImpact(context.Background())
	if report.Status != webexecution.SemanticImpactStatusNoBaseline || report.Digest == "" {
		t.Fatalf("first deployment report = %#v", report)
	}
}

type semanticImpactSnapshotStore struct {
	latest *snapshot.Snapshot
	files  map[string]string
}

func (s *semanticImpactSnapshotStore) Latest(context.Context, string) (*snapshot.Snapshot, error) {
	return s.latest, nil
}

func (s *semanticImpactSnapshotStore) ValidateMetadata(context.Context, string, string) (snapshot.Snapshot, error) {
	return snapshot.Snapshot{}, nil
}

func (s *semanticImpactSnapshotStore) MaterializeForPipelineExecution(_ context.Context, _, _ string, destination string) error {
	for path, content := range s.files {
		fullPath := filepath.Join(destination, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func semanticImpactPipelineFiles(upstreamType string) map[string]string {
	return map[string]string{
		"pipeline.yml": "name: analytics\n",
		"assets/lineitems.sql": `/* @bruin
name: analytics.lineitems
type: duckdb.sql
columns:
  - name: total_amount
    type: ` + upstreamType + `
@bruin */
SELECT CAST(1 AS ` + upstreamType + `) AS total_amount
`,
		"assets/revenue.sql": `/* @bruin
name: analytics.revenue
type: duckdb.sql
depends:
  - analytics.lineitems
@bruin */
SELECT SUM(total_amount) AS total FROM analytics.lineitems
`,
	}
}

func writeSemanticImpactFile(t *testing.T, root, path, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
