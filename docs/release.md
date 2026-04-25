# Release Operations

Releases are tag-driven.

## Create A Release

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

The release workflow runs tests, race tests, vet, formatting checks, builds cross-platform binaries, creates archives, writes checksums, and uploads artifacts to the GitHub Release.

## Create A Prerelease

Use a semantic prerelease tag:

```sh
git tag vX.Y.Z-rc.1
git push origin vX.Y.Z-rc.1
```

The workflow marks tags containing a hyphen as prereleases.

## Verify Artifacts

Download the archive and checksum file from the GitHub Release, then run:

```sh
sha256sum -c udp46_checksums.txt
```

Print embedded metadata:

```sh
udp46 --version
```

The version, commit, and date match the `udp46_build_info` metric when metrics are enabled.

## Install Packages

Linux archives contain the `udp46` binary, documentation, and MIT license. When `.deb` artifacts are present:

```sh
dpkg -i udp46_*_linux_amd64.deb
```

Packages install the binary and example documentation. Review the systemd unit before enabling it.

## Rollback

`udp46` stores session mappings only in memory. Rolling back restarts the daemon and drops active mappings. WireGuard clients using `PersistentKeepalive = 25` recover on the next keepalive or outbound packet.
