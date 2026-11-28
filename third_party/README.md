# Third-party code

## anacrolix-torrent

[github.com/anacrolix/torrent](https://github.com/anacrolix/torrent) v1.63.0 (MPL-2.0, see its `LICENSE`), used
through a `replace` directive in `go.mod`. Tests and command-line tools are left out. Changes, each marked
`Magnetar patch`:

- `torrent.go` (`newConnsAllowed`): new peer connections stay allowed while a download speed limit is saturated.
  Upstream refuses them, which stalls seeding and peer discovery whenever a limit or the alternative speed is on.
- `magnetar-limits.go` (new), `torrent.go` (`limits` field), `peerconn.go` (`mainReadLoop`, `upload`) and the web
  seed reader in `torrent.go`: per-torrent download and upload limits (`Torrent.SetRateLimits`), applied on top of the
  client-wide limits to peer and web seed traffic.
- `magnetar-extras.go` (new): `Torrent.TrackerStatuses` (structured tracker status) and `Client.BanPeerIP`
  (exported wrapper that takes the client lock), `Torrent.Health` (distributed copies and pieces no connected peer
  has).
- `storage/file-io-classic.go`: cached file handles are reference counted, so closing or renaming a file can no
  longer close a handle that a write or piece hash is still using ("file already closed", which disabled a whole
  torrent's download). `storage/file-io-classic_test.go` (new) covers it and runs in CI on Linux and Windows.
- `storage/open_windows.go`, `storage/open_other.go` (new), `storage/file-io-common.go`, `storage/file-misc.go`:
  on Windows, files are opened with `FILE_SHARE_DELETE` so a finished part file can be renamed while it is being
  read, and new files are sparse so writing far into a large file does not first zero-fill everything before it.
- `storage/sys_windows.go` (`msync`): flushes only the written range of a memory-mapped file instead of the whole
  file and its disk buffers.

To update: copy the new release over this folder (without `*_test.go`, `testdata`, `cmd`, `analysis`), re-apply the
patches above (keep `storage/file-io-classic_test.go`), and run `make test`.
