# Comuse controlled accessibility fixture

This experimental macOS 14+ source fixture presents only synthetic controls for controlled accessibility checks. It is not product UI, real application coverage, or a claim that accessibility works on every macOS release. It does not capture pixels, use the network, inspect other apps, or read real application content. Do not enter real credentials or personal data.

## Build and launch

From the repository root, build a new app bundle and all Swift build products on the external volume:

```sh
export COMUSE_ARTIFACT_ROOT=/path/on/the/operator-verified-external-volume
spikes/macos/fixture/build-app.sh \
  "$COMUSE_ARTIFACT_ROOT/comuse-fixture-build" \
  "$COMUSE_ARTIFACT_ROOT/comuse-fixture/ComuseFixture.app"
open -n "$COMUSE_ARTIFACT_ROOT/comuse-fixture/ComuseFixture.app" \
  --args --fixture-nonce run-a
```

Choose a fresh output app path for each package. The script refuses to overwrite one. The bundle identifier is `com.sirerun.comuse.fixture`; the single window title is exactly `Comuse Fixture <nonce>`. The nonce is required and limited to 1–64 ASCII letters, digits, `.`, `_`, or `-`. Packaging applies an ad-hoc signature for local experimental use; this is unsigned with respect to a Developer ID and is not a release artifact. The package declares macOS 14 as its compilation/deployment baseline; this does not qualify runtime behavior on macOS 14.

Run core state/configuration tests or build the debug app package with the SwiftPM scratch path on the external volume:

```sh
swift test --package-path spikes/macos/fixture \
  --scratch-path "$COMUSE_ARTIFACT_ROOT/comuse-fixture-test-cache" --jobs 2
```

## Stable accessibility controls and postconditions

| Identifier | Role / synthetic behavior |
| --- | --- |
| `label` | Static text identifying the fixture. |
| `buttoncounter` | Button increments the adjacent `counter-value` static text from `Counter: 0` by one per activation. |
| `textfield` | Editable text field starts with `synthetic text`; edits remain local to this process. |
| `securefield` | Secure editable text field starts with `synthetic-secret`; the field masks it on screen. |
| `scrollsentinel` | Static text at the bottom of a scroll view after 30 synthetic rows. |
| `delayedlabel` | Static text changes from `Waiting for deterministic delayed state` to `Delayed state ready` after 500 ms. |
| `removechild` | Button removes the sibling `removechild-target`; `removechild-result` appears. |
| `focuseditinterference` | Button replaces the text field with `synthetic external edit N` and asks the text field to take focus, modeling a controlled edit/focus conflict. |

`scrollcontainer`, `counter-value`, `removechild-target`, and `removechild-result` are additional stable identifiers for deterministic assertions. All state is synthetic and predictable; no values are sourced from the environment. AX verification must first confirm the expected process bundle identity and the exact nonce-scoped top-level window title before traversing descendants. Use semantic roles and identifiers; no pixel capture or unrelated application inspection is part of this fixture lane.
