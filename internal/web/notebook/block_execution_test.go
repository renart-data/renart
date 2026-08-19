package notebook

import "testing"

func TestTabularArtifactRequiresHonestPublicationStateAndTypedSchema(t *testing.T) {
	valid := TabularArtifact{
		Schema:   []TabularColumn{{Name: "amount", Type: "DECIMAL(18,2)"}},
		Complete: true,
	}
	if err := valid.ValidateForPublication(); err != nil {
		t.Fatalf("complete typed artifact rejected: %v", err)
	}
	valid.Complete = false
	valid.Sampled = true
	if err := valid.ValidateForPublication(); err != nil {
		t.Fatalf("explicit sample rejected: %v", err)
	}
	valid.Sampled = false
	if err := valid.ValidateForPublication(); err == nil {
		t.Fatal("ambiguous partial artifact was accepted")
	}
	valid.Complete = true
	valid.Sampled = true
	if err := valid.ValidateForPublication(); err == nil {
		t.Fatal("artifact marked complete and sampled was accepted")
	}
	valid.Sampled = false
	valid.Schema[0].Type = ""
	if err := valid.ValidateForPublication(); err == nil {
		t.Fatal("untyped artifact was accepted")
	}
}
