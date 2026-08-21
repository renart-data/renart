import * as fs from "node:fs";
import * as path from "node:path";
import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;
let extensionContext: vscode.ExtensionContext | undefined;

export async function activate(context: vscode.ExtensionContext) {
  extensionContext = context;
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration(async (event) => {
      if (event.affectsConfiguration("renartSqlLsp")) {
        await restartClient();
      }
    }),
  );
  await startClient(context);
}

async function startClient(context: vscode.ExtensionContext) {
  const folder = pickWorkspaceFolder();
  if (!folder) {
    return;
  }
  if (client) {
    return;
  }

  const config = vscode.workspace.getConfiguration("renartSqlLsp");
  const command = config.get<string>("serverPath") || "renart";
  const args = ["sql-lsp", "--workspace", folder.uri.fsPath];

  const serverOptions: ServerOptions = {
    command,
    args,
    transport: TransportKind.stdio,
    options: {
      cwd: folder.uri.fsPath,
    },
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [
      { scheme: "file", language: "sql" },
      { scheme: "file", pattern: "**/*.sql" },
    ],
    workspaceFolder: folder,
    outputChannelName: "Renart SQL LSP",
    synchronize: {
      fileEvents: vscode.workspace.createFileSystemWatcher("**/*.{sql,yml,yaml}"),
    },
  };

  client = new LanguageClient(
    "renartSqlLsp",
    "Renart SQL LSP",
    serverOptions,
    clientOptions,
  );

  context.subscriptions.push(client);
  await client.start();
}

export async function deactivate() {
  await stopClient();
  extensionContext = undefined;
}

async function restartClient() {
  await stopClient();
  if (extensionContext) {
    await startClient(extensionContext);
  }
}

async function stopClient() {
  const running = client;
  client = undefined;
  if (running) {
    await running.stop();
  }
}

function pickWorkspaceFolder() {
  const folders = vscode.workspace.workspaceFolders ?? [];
  return folders.find((folder) => isPipelineWorkspace(folder.uri.fsPath)) ?? folders[0];
}

function isPipelineWorkspace(root: string) {
  if (exists(root, "dbt_project.yml") || exists(root, "dbt_project.yaml")) {
    return true;
  }
  return containsFile(root, "pipeline.yml") || containsFile(root, "pipeline.yaml");
}

function exists(root: string, relativePath: string) {
  return fs.existsSync(path.join(root, relativePath));
}

function containsFile(root: string, fileName: string) {
  const stack = [root];
  while (stack.length > 0) {
    const current = stack.pop();
    if (!current) {
      continue;
    }
    let entries: fs.Dirent[];
    try {
      entries = fs.readdirSync(current, { withFileTypes: true });
    } catch {
      continue;
    }
    for (const entry of entries) {
      if (entry.name === ".git" || entry.name === "node_modules" || entry.name === "target") {
        continue;
      }
      const entryPath = path.join(current, entry.name);
      if (entry.isFile() && entry.name === fileName) {
        return true;
      }
      if (entry.isDirectory()) {
        stack.push(entryPath);
      }
    }
  }
  return false;
}
