export type WorkspaceSaveParticipant = () => Promise<void>;

const participants = new Set<WorkspaceSaveParticipant>();
const trackedSaves = new Set<Promise<unknown>>();
type RetiredWorkspaceSave = {
  participant: WorkspaceSaveParticipant;
  promise: Promise<void>;
  failed: boolean;
};

/**
 * Register an already-started workspace mutation with the next run/deploy
 * barrier. This covers blur-committed metadata transactions: the blur event
 * starts the request before the button click opens review, so review must wait
 * for that request just as it waits for an editor flush.
 */
export function trackWorkspaceSave<T>(save: Promise<T>): Promise<T> {
  trackedSaves.add(save);
  void save.then(
    () => trackedSaves.delete(save),
    () => trackedSaves.delete(save),
  );
  return save;
}
const retiredSaves = new Set<RetiredWorkspaceSave>();

function startRetiredWorkspaceSave(retiredSave: RetiredWorkspaceSave) {
  const promise = Promise.resolve().then(retiredSave.participant);
  retiredSave.promise = promise;
  retiredSave.failed = false;

  void promise.then(
    () => {
      if (retiredSave.promise === promise) {
        retiredSaves.delete(retiredSave);
      }
    },
    () => {
      if (retiredSave.promise === promise) {
        retiredSave.failed = true;
      }
    },
  );
}

/**
 * Keep an editor's final save visible to the next workspace operation after
 * that editor has unmounted and is no longer an active participant.
 */
export function retireWorkspaceSaveParticipant(participant: WorkspaceSaveParticipant) {
  const retiredSave: RetiredWorkspaceSave = {
    participant,
    promise: Promise.resolve(),
    failed: false,
  };
  retiredSaves.add(retiredSave);
  startRetiredWorkspaceSave(retiredSave);
}

export function registerWorkspaceSaveParticipant(participant: WorkspaceSaveParticipant) {
  participants.add(participant);
  return () => {
    participants.delete(participant);
  };
}

/**
 * Flush and await every mounted editor that can have a local workspace write
 * pending. Run/deploy actions call this before resolving their source so the
 * filesystem snapshot includes the latest editor state.
 */
export async function awaitWorkspaceSaves() {
  const failures: unknown[] = [];
  const recordFailures = (results: PromiseSettledResult<unknown>[]) => {
    for (const result of results) {
      if (
        result.status === "rejected" &&
        !failures.some((failure) => Object.is(failure, result.reason))
      ) {
        failures.push(result.reason);
      }
    }
  };

  recordFailures(
    await Promise.allSettled(
      Array.from(participants, (participant) => Promise.resolve().then(participant)),
    ),
  );

  // A property field can start a semantic transaction from its blur handler
  // immediately before the run/deploy click. Drain the batch that existed when
  // the barrier began, plus any follow-up writes started while it was waiting.
  while (trackedSaves.size > 0) {
    recordFailures(await Promise.allSettled(Array.from(trackedSaves)));
  }

  // A participant can unmount while the mounted saves above are in flight.
  // Drain retired saves until the set is stable so navigation cannot let a
  // run/deploy action overtake the editor's final write.
  const observedRetiredSaves = new Set<RetiredWorkspaceSave>();
  while (true) {
    const retired = Array.from(retiredSaves).filter(
      (retiredSave) => !observedRetiredSaves.has(retiredSave),
    );
    if (retired.length === 0) {
      break;
    }
    for (const retiredSave of retired) {
      observedRetiredSaves.add(retiredSave);
      // Keep a failed, unmounted editor registered so the next operation gets
      // one opportunity to retry its pending value instead of silently moving
      // on with the stale file.
      if (retiredSave.failed) {
        startRetiredWorkspaceSave(retiredSave);
      }
    }
    const results = await Promise.allSettled(retired.map(({ promise }) => promise));
    recordFailures(results);
  }

  // Retired editor flushes may themselves trigger a semantic follow-up write.
  while (trackedSaves.size > 0) {
    recordFailures(await Promise.allSettled(Array.from(trackedSaves)));
  }

  if (failures.length === 1) {
    throw failures[0];
  }
  if (failures.length > 1) {
    throw new AggregateError(failures, "Could not save all workspace changes");
  }
}
