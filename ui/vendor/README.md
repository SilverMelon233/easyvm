# vendored front ends, copied here so they can be retired upstream

THESE ARE COPIES, AND THEY ARE HERE TO BE THE LAST COPY.

`easystream` and `easyworker` each grew a browser UI of their own, and each UI was correct for the
job those daemons had at the time: one showed a screen, one ran commands. What changed is that
`easyvm` now owns ALL of the browser surface - a single page with a screen panel, a serial console,
an ssh shell, guest files and jobs - and a second and third UI behind it are three front ends to
keep in step with one backend each.

The plan these copies serve:

1. take the UI out of `easystream` (its HTTP listener goes with it; the daemon keeps `est://` only)
2. take the UI out of `easyworker` (it keeps the Connect RPC only)
3. build the one UI in `ui/` here, against the protocols rather than against anybody's page

## What was taken, and from where

| directory | source | commit |
|---|---|---|
| `easystream-webui/webui/` | `easydev-org/easystream-webui`, path `webui/` | main |
| `easyworker-webui/` | `easydev-org/easyworker-webui`, repo root | main |

Both were cloned at `main` on 2026-11-19. The upstream repositories are NOT modified by this copy;
they are the reference for behaviour and the source of anything worth keeping.

## What each one is worth

`easystream-webui/webui/app.js` is the useful half of this directory: it is a working WebCodecs
`VideoDecoder` client for est - handshake, Hello parsing, Annex-B keyframe detection, the
`optimizeForLatency` flag, and the KeyFrameReq that turns a lost frame into one bad frame instead of
two seconds of smear. `easyvm`'s own panel needs exactly that code and had re-derived some of it
badly (it sent `0x03` where `KeyFrameReq` is `0x0A`, which is `Auth` - a bug whose only symptom is
"control stopped working after a decode error").

`easyworker-webui/src/` is a Svelte 5 app: `ShellPage`, `FilesDrawer`, `JobsDrawer` and the Connect
RPC client generated from `worker.proto`. It is worth reading for the SHAPE of a file browser and a
job console, and it is not worth keeping as a build: it needs `npm`, a toolchain, and a node stage in
the Containerfile, all to render a tree of paths and a stream of output that `easyvm` can draw in a
few hundred lines of DOM.
