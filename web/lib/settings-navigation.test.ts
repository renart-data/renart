import { describe, expect, it } from "vitest";
import {
  settingsEditorIdentity,
  normalizeSettingsSearch,
  filterSettingsEnvironments,
} from "./settings-navigation";

describe("settings navigation", () => {
  it("adding the current project to a field link does not leave the draft", () => {
    const current = {
      pathname: "/project/connections",
      search: { environment: "dev", connection: "db" },
    };
    const next = { ...current, search: { ...current.search, project: "current" } };
    expect(settingsEditorIdentity(current, "current")).toBe(
      settingsEditorIdentity(next, "current"),
    );
    expect(settingsEditorIdentity(current, "other")).not.toBe(
      settingsEditorIdentity(next, "other"),
    );
  });
  it("retains addressable item and create/clone state, never arbitrary form data", () => {
    expect(
      normalizeSettingsSearch(
        { environment: "prod", connection: "warehouse", action: "create", password: "secret" },
        "connections",
      ),
    ).toEqual({ environment: "prod", connection: "warehouse", action: "create" });
    expect(normalizeSettingsSearch({ environment: [], action: "clone" }, "connections")).toEqual({
      environment: undefined,
      connection: undefined,
      action: undefined,
    });
    expect(
      normalizeSettingsSearch({ environment: "dev", action: "clone" }, "environments").action,
    ).toBe("clone");
  });

  it("does not discard a draft on field-only navigation or unrelated panel changes", () => {
    const current = {
      pathname: "/project/connections",
      search: { project: "p", environment: "dev", connection: "db" },
    };
    const field = {
      ...current,
      search: {
        ...current.search,
        result: "inspect",
        detail: {
          v: 1,
          environment: "dev",
          target: { kind: "connection", connection: "db", field: "host" },
        },
      },
    };
    expect(settingsEditorIdentity(field)).toBe(settingsEditorIdentity(current));
    expect(
      settingsEditorIdentity({ ...current, search: { ...current.search, environment: "prod" } }),
    ).not.toBe(settingsEditorIdentity(current));
    expect(
      settingsEditorIdentity({ ...current, search: { ...current.search, project: "other" } }),
    ).not.toBe(settingsEditorIdentity(current));
  });

  it("uses a connection locator's explicit identity and distinguishes creation from editing", () => {
    expect(
      settingsEditorIdentity({
        pathname: "/project/connections",
        search: {
          environment: "wrong",
          connection: "wrong",
          detail: { environment: "prod", target: { kind: "connection", connection: "db" } },
        },
      }),
    ).toBe(
      settingsEditorIdentity({
        pathname: "/project/connections",
        search: { environment: "prod", connection: "db" },
      }),
    );
    expect(
      settingsEditorIdentity({
        pathname: "/project/environments",
        search: { environment: "prod", action: "clone" },
      }),
    ).not.toBe(
      settingsEditorIdentity({
        pathname: "/project/environments",
        search: { environment: "prod" },
      }),
    );
  });

  it("filters connection names, types and environments without dropping the selected item", () => {
    const environments = [
      { name: "dev", connections: [{ name: "warehouse", type: "duckdb", values: {} }] },
      {
        name: "prod",
        connections: [
          { name: "warehouse", type: "postgres", values: {} },
          { name: "lake", type: "s3", values: {} },
        ],
      },
    ];
    expect(
      filterSettingsEnvironments(environments, "post", {
        environment: "dev",
        connection: "warehouse",
      }),
    ).toEqual([
      environments[0],
      { ...environments[1], connections: [environments[1].connections[0]] },
    ]);
    expect(filterSettingsEnvironments(environments, "PROD")).toEqual([environments[1]]);
    expect(filterSettingsEnvironments(environments, "absent")).toEqual([]);
  });
});
