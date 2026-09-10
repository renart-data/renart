import { expect } from "@playwright/test";
import { liveTest as test } from "../live-app-fixture";
import type { WorkspaceConfigResponse } from "../../../lib/types";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

test("connection settings retain credential source when a provider is locked or unavailable", async ({
  page,
  liveApp,
}) => {
  // Exercise the real settings form with controlled status-only API responses.
  // The host keyring is never read, locked, unlocked, or written by this test.
  let state = "permission_required";
  await page.route("**/api/config", async (route) => {
    const response = await route.fetch();
    const config = (await response.json()) as WorkspaceConfigResponse;
    config.environments
      .find((environment) => environment.name === "default")!
      .connections.push({
        name: "storage",
        type: "s3",
        values: { bucket_name: "fixture" },
        secret_fields: Object.fromEntries(
          ["access_key_id", "secret_access_key"].map((field) => [
            field,
            {
              status: state,
              provider: "local",
              reference: `local:storage/${field}`,
              writable: state === "configured",
              rotatable: state === "configured",
              message: state === "configured" ? "" : "The system credential store needs attention.",
            },
          ]),
        ),
      });
    await route.fulfill({ response, json: config });
  });
  for (state of ["permission_required", "unavailable", "configured"]) {
    await page.goto(
      `${liveApp.baseURL}/project/connections?environment=default&connection=storage`,
    );
    await page.getByRole("button", { name: "Credentials", exact: true }).click();
    const source = page.getByRole("radiogroup", {
      name: "secret_access_key secret source",
      exact: true,
    });
    await expect(
      source.getByRole("radio", { name: "Credential store", exact: true }),
    ).toBeChecked();
    await expect(source.getByRole("radio", { name: "Environment", exact: true })).not.toBeChecked();
    await expect(page.getByLabel("secret_access_key", { exact: true })).toHaveAttribute(
      "type",
      "password",
    );
    await expect(page.getByText("Environment ref", { exact: true })).toHaveCount(0);
  }
});
