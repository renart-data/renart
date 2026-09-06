// A mounted editor may reflect its own selection without revealing it again.
// This marker is not durable UI state: reloads, other navigation and history
// traversal must reveal the addressed resource normally.
export class LocalResourceReflection {
  private token: string | undefined;

  begin(token: string) {
    this.token = token;
  }

  get active() {
    return this.token !== undefined;
  }

  observe(action: string, token: string | undefined) {
    if (action !== "REPLACE" || token !== this.token) this.token = undefined;
  }

  matches(token: string | undefined) {
    return token !== undefined && token === this.token;
  }
}
