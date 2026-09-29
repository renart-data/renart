# Review and release flow: follow-ups

Status: the review, deploy and schedule flow was implemented on branch
`fix-jinja-syntax-errors` on 28 September 2026. As-built behaviour lives in
[frontend](../architecture/frontend.md) (review dialog),
[backend](../architecture/backend.md) (plan issue de-duplication and template
errors) and [staleness](../architecture/staleness.md) §5–6 (deploy with
schedules, schedule row actions). This file keeps only what is left.

## Point from a successful run to Deploy

After a manual run succeeds while the working tree differs from the latest
deployment, nothing suggests deploying. The app has no toast or notification
surface, and adding one only for this is not worth it yet. Revisit this when a
notification surface exists, or show it in the run results panel.
