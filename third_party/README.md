# Third-party code

## anacrolix-torrent

[github.com/anacrolix/torrent](https://github.com/anacrolix/torrent) v1.63.0 (MPL-2.0, see its `LICENSE`), used
through a `replace` directive in `go.mod`. Tests and command-line tools are left out. Changes, each marked
`Magnetar patch`:

- `torrent.go` (`newConnsAllowed`): new peer connections stay allowed while a download speed limit is saturated.
  Upstream refuses them, which stalls seeding and peer discovery whenever a limit or the alternative speed is on.

To update: copy the new release over this folder (without `*_test.go`, `testdata`, `cmd`, `analysis`), re-apply the
patches above, and run `make test`.
