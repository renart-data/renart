package service

import "renart/internal/web/notebookdoc"

// TransactionHook is retained privately for focused service tests and the
// promotion adapter while notebookdoc owns the recovery journal itself.
type notebookTransactionHook = notebookdoc.TransactionHook

func readNotebookAuthoredFiles(dir string) (map[string][]byte, error) {
	return notebookdoc.ReadAuthoredFiles(dir)
}

func applyNotebookFileTransaction(
	workspaceRoot, notebookDir string,
	before, after map[string][]byte,
	hook notebookTransactionHook,
) error {
	return notebookdoc.ApplyNotebookFileTransaction(workspaceRoot, notebookDir, before, after, hook)
}

func applyWorkspaceFileTransaction(
	workspaceRoot string,
	before, after map[string][]byte,
	hook notebookTransactionHook,
) error {
	return notebookdoc.ApplyWorkspaceFileTransaction(workspaceRoot, before, after, hook)
}

func recoverNotebookFileTransactions(workspaceRoot string) error {
	return notebookdoc.RecoverFileTransactions(workspaceRoot)
}
