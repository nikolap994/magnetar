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
  (exported wrapper that takes the client lock).

To update: copy the new release over this folder (without `*_test.go`, `testdata`, `cmd`, `analysis`), re-apply the
patches above, and run `make test`.
