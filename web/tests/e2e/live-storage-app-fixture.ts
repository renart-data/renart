import type { WorkspaceConnectionSecretChanges } from "../../lib/types";
import { liveTest } from "./live-app-fixture";
import { createLiveStorage } from "./live-storage-fixture";

export const storageTest = liveTest.extend<{
  storage: Awaited<ReturnType<typeof createLiveStorage>>;
  largeS3Listing: boolean;
}>({
  largeS3Listing: [false, { option: true }],
  // Docker networking must be ready before Chromium opens the page.
  storage: [
    async ({ largeS3Listing }, use) => {
      const storage = await createLiveStorage({ largeS3Listing });
      try {
        await use(storage);
      } finally {
        await storage.dispose();
      }
    },
    { auto: true, timeout: 60000 },
  ],
  liveAppEnv: async ({ storage }, use) => {
    await use({
      RENART_STORAGE_TEST_ACCESS_KEY: "renart",
      RENART_STORAGE_TEST_SECRET_KEY: "renart-secret",
      RENART_STORAGE_TEST_PASSWORD: storage.password,
      // Match a headless CI runner even when the host has an unlocked keyring.
      DBUS_SESSION_BUS_ADDRESS: "unix:path=/renart-e2e-no-session-bus",
    });
  },
});

export function storageSecretChanges(provider: "s3" | "sftp"): WorkspaceConnectionSecretChanges {
  const binding = (name: string) => ({
    action: "replace" as const,
    binding: { ref: `env:${name}` },
  });
  return provider === "s3"
    ? {
        access_key_id: binding("RENART_STORAGE_TEST_ACCESS_KEY"),
        secret_access_key: binding("RENART_STORAGE_TEST_SECRET_KEY"),
      }
    : { password: binding("RENART_STORAGE_TEST_PASSWORD") };
}
