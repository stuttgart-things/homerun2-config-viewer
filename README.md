# homerun2-config-viewer

Read-only view of which homerun2 alert triggers what in which catcher.

What a message does in a homerun2 cluster is decided in two invisible places:
the streams and consumer group each component runs with, and each catcher's
profile. `homerun2-config-viewer` collects both from the Kubernetes API —
Deployments labelled `app.kubernetes.io/part-of=homerun2` and their profile
ConfigMaps — and shows:

- **Components**: role, streams, consumer group, profile status
- **Findings**: streams nobody reads, shared consumer groups, severities no
  catcher reacts to, rules that cannot act
- **Matrix**: severity × system → which catcher reacts how
- **Dry run**: type a sample message and see what each catcher would do,
  without publishing anything

It never talks to Redis and never reads Secrets. Rule evaluation comes from the
[`routing`](https://github.com/stuttgart-things/homerun-library/blob/main/docs/routing.md)
package of homerun-library.

Background: [stuttgart-things/homerun-library#122](https://github.com/stuttgart-things/homerun-library/issues/122)

> Work in progress — see the issues for the build-out.
