import type { IDisposable } from "monaco-editor";
import { configureMonacoYaml, type JSONSchema } from "monaco-yaml";
import YamlWorker from "./unit-test-yaml.worker?worker";

type YamlMonaco = Parameters<typeof configureMonacoYaml>[0];
type Monaco = typeof import("monaco-editor");

// Keep this language service inside the fixture editor. In particular it must
// not add diagnostics or replace completions/configuration in asset YAML tabs.
export function registerUnitTestYAML(
  monaco: Monaco,
  path: string,
  schema: JSONSchema,
): IDisposable {
  const matches = (model: { uri: { toString(): string } }) => model.uri.toString() === path;
  const selector = {
    language: "yaml",
    scheme: "renart-unit-tests",
    pattern: new URL(path).pathname,
  };
  const languages = new Proxy(monaco.languages, {
    get(target, key) {
      const value = Reflect.get(target, key);
      if (key === "register" || key === "setLanguageConfiguration") return () => ({ dispose() {} }); // Monaco already owns YAML highlighting/configuration.
      if (
        typeof key === "string" &&
        key.startsWith("register") &&
        key.endsWith("Provider") &&
        typeof value === "function"
      )
        return (_language: unknown, ...args: unknown[]) =>
          Reflect.apply(value, target, [selector, ...args]);
      return value;
    },
  });
  const scoped = {
    ...monaco,
    languages,
    editor: {
      ...monaco.editor,
      getModels: () => monaco.editor.getModels().filter(matches),
      onDidCreateModel: (callback: Parameters<Monaco["editor"]["onDidCreateModel"]>[0]) =>
        monaco.editor.onDidCreateModel((model) => {
          if (matches(model)) callback(model);
        }),
      onWillDisposeModel: (callback: Parameters<Monaco["editor"]["onWillDisposeModel"]>[0]) =>
        monaco.editor.onWillDisposeModel((model) => {
          if (matches(model)) callback(model);
        }),
      onDidChangeModelLanguage: (
        callback: Parameters<Monaco["editor"]["onDidChangeModelLanguage"]>[0],
      ) =>
        monaco.editor.onDidChangeModelLanguage((event) => {
          if (matches(event.model)) callback(event);
        }),
      createWebWorker: (options: { createData?: unknown; keepIdleModels?: boolean }) => {
        // monaco-yaml 5 uses the legacy two-message initialization protocol.
        // Monaco 0.56 accepts an owned Worker instead of moduleId/createData.
        // Adapt only this plugin; do not replace global MonacoEnvironment.
        const worker = new YamlWorker();
        worker.postMessage("ignore");
        worker.postMessage(options.createData);
        return monaco.editor.createWebWorker({ worker, keepIdleModels: options.keepIdleModels });
      },
    },
  };
  return configureMonacoYaml(scoped as unknown as YamlMonaco, {
    enableSchemaRequest: false,
    yamlVersion: "1.2",
    schemas: [{ uri: `${path}.schema.json`, fileMatch: [path], schema }],
  }) as unknown as IDisposable;
}
