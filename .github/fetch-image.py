#!/usr/bin/env python3
"""Pull an OCI image's amd64 layers and cat them into one tar.

    fetch-image.py <repo> <tag> <out.tar>

THERE IS NO DOCKER HERE AND NONE IS NEEDED. A root filesystem is one or more tar
layers in a registry; this is the four HTTP requests `docker pull` makes, written
out so a build script can depend on them and so the digest is visible.
"""
import json
import sys
import urllib.request

REG = "https://registry-1.docker.io/v2"
AUTH = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:%s:pull"


def get(url, headers=None):
    req = urllib.request.Request(url, headers=headers or {})
    return urllib.request.urlopen(req, timeout=120).read()


def main():
    if len(sys.argv) != 4:
        sys.exit("usage: fetch-image.py <repo> <tag> <out.tar>")
    repo, tag, out = sys.argv[1:]

    token = json.loads(get(AUTH % repo))["token"]
    h = {"Authorization": "Bearer " + token}

    ACCEPT = ("application/vnd.oci.image.index.v1+json,"
              "application/vnd.docker.distribution.manifest.list.v2+json")
    h_idx = dict(h, Accept=ACCEPT)
    body = get("%s/%s/manifests/%s" % (REG, repo, tag), h_idx)

    # A SINGLE-PLATFORM IMAGE HAS NO index, only a manifest. Both are valid answers
    # to the same request, so the shape is decided by whether `manifests` is present.
    digest = None
    if b'"manifests"' in body:
        for m in json.loads(body)["manifests"]:
            p = m.get("platform", {})
            if p.get("architecture") == "amd64" and p.get("os") == "linux":
                digest = m["digest"]
                break
    else:
        digest = tag

    h_man = dict(h, Accept="application/vnd.oci.image.manifest.v1+json,"
                           "application/vnd.docker.distribution.manifest.v2+json")
    man = json.loads(get("%s/%s/manifests/%s" % (REG, repo, digest), h_man))

    with open(out, "wb") as f:
        for layer in man["layers"]:
            f.write(get("%s/%s/blobs/%s" % (REG, repo, layer["digest"]), h))
    print("%s:%s -> %s (%d layer(s), digest %s)"
          % (repo, tag, out, len(man["layers"]), digest))


if __name__ == "__main__":
    main()