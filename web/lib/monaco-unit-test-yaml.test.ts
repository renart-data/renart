import { describe, expect, it, vi } from "vitest";
import { registerUnitTestYAML } from "./monaco-unit-test-yaml";

const captured = vi.hoisted(() => ({
  monaco: null as any,
  options: null as any,
  messages: [] as unknown[],
}));
vi.mock("monaco-yaml", () => ({
  configureMonacoYaml: (monaco: unknown, options: unknown) => {
    captured.monaco = monaco;
    captured.options = options;
    return { dispose: vi.fn() };
  },
}));
vi.mock("./unit-test-yaml.worker?worker", () => ({
  default: class {
    postMessage(value: unknown) {
      captured.messages.push(value);
    }
  },
}));

describe("fixture-scoped YAML language service", () => {
  it("does not reconfigure other YAML models or global worker factories", () => {
    const fixture = { uri: { toString: () => "renart-unit-tests://fixtures/one.yaml" } };
    const other = { uri: { toString: () => "file:///assets/source.yml" } };
    const listeners: Record<string, (value: any) => void> = {};
    const register = vi.fn();
    const languageConfiguration = vi.fn();
    const complete = vi.fn();
    const createWorker = vi.fn();
    const monaco = {
      languages: {
        register,
        setLanguageConfiguration: languageConfiguration,
        registerCompletionItemProvider: complete,
      },
      editor: {
        getModels: () => [fixture, other],
        onDidCreateModel: (fn: any) => {
          listeners.create = fn;
        },
        onWillDisposeModel: (fn: any) => {
          listeners.dispose = fn;
        },
        onDidChangeModelLanguage: (fn: any) => {
          listeners.language = fn;
        },
        createWebWorker: createWorker,
      },
    };
    registerUnitTestYAML(monaco as any, fixture.uri.toString(), { type: "object" });
    expect(captured.monaco.editor.getModels()).toEqual([fixture]);
    const observed = vi.fn();
    captured.monaco.editor.onDidCreateModel(observed);
    listeners.create(other);
    expect(observed).not.toHaveBeenCalled();
    listeners.create(fixture);
    expect(observed).toHaveBeenCalledWith(fixture);
    captured.monaco.languages.register({ id: "yaml" });
    captured.monaco.languages.setLanguageConfiguration("yaml", {});
    expect(register).not.toHaveBeenCalled();
    expect(languageConfiguration).not.toHaveBeenCalled();
    captured.monaco.languages.registerCompletionItemProvider("yaml", { provider: true });
    expect(complete).toHaveBeenCalledWith(
      { language: "yaml", scheme: "renart-unit-tests", pattern: "/one.yaml" },
      { provider: true },
    );
    captured.monaco.editor.createWebWorker({ createData: { schema: true } });
    expect(captured.messages).toEqual(["ignore", { schema: true }]);
    expect(createWorker).toHaveBeenCalledWith({
      worker: expect.any(Object),
      keepIdleModels: undefined,
    });
    expect(captured.options.enableSchemaRequest).toBe(false);
    expect(captured.options.schemas[0].fileMatch).toEqual([fixture.uri.toString()]);
  });
});
