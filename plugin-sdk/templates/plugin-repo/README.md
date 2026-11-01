# TokenRouter plugin template

A daily check-in plugin with an idempotent wallet reward, retained host KV records,
and user/admin pages. Go and browser SDKs are MIT licensed. Plugin code runs with
the administrator's trust and is not an operating-system or browser sandbox.

## Initialize your repository

1. Publish the Go SDK tag `plugin-sdk/go/v0.1.0` and npm package
   `@tokenrouter/plugin-sdk@0.1.0`, or select already published compatible versions.
   This seed does not create those external releases.
2. Replace `github.com/example/tr-plugin-checkin` in `go.mod` and `cmd/plugin/main.go`
   with your repository path. Choose your own publisher prefix and change
   `example.checkin` in `plugin.json` and `web/vite.config.ts` together.
3. Update the publisher, homepage, descriptions, license, and declared permissions.
4. Run `make bootstrap`, then commit `go.sum` and `web/pnpm-lock.yaml`. Release builds
   intentionally require the committed browser lockfile and do not generate it.
5. Run `make test pack`. Publish `plugin.json`, `checksums.txt`, and every archive in
   `release/` together. Tag `v1.0.0` only when it matches the manifest version.

## Develop against TokenRouter

Run `make dev`, enable `plugins.dev_mode`, and add the absolute `dev/` directory to
`plugins.dev_dirs`. Restart the plugin after rebuilding. The browser SDK preset
also supplies a standalone mock when running `pnpm --dir web dev`; mock balances
and records do not establish real host persistence.

Configure a small reward and matching administrator wallet limits before enabling
real rewards. The example claims each user's day in KV before crediting, stores
its chosen amount, and retries uncertain credit with the same idempotency key.
Configuration changes affect new claims; completed claims remain completed.

Business records survive ordinary uninstall/reinstall when the administrator keeps
plugin data. State directories are disposable. Frontend assets are public, so
credentials and secret configuration must never be bundled in them.

The calendar artwork comes from [Lucide](https://github.com/lucide-icons/lucide/blob/66d8f9fc394b8530377e5f6112f0b8908ba01280/icons/calendar.svg).
Its license and attribution are retained below and in `assets/LICENSE.lucide`.

```text
ISC License

Copyright (c) 2026 Lucide Icons and Contributors

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

---

The following Lucide icons are derived from the Feather project:

airplay, alert-circle, alert-octagon, alert-triangle, aperture, arrow-down-circle, arrow-down-left, arrow-down-right, arrow-down, arrow-left-circle, arrow-left, arrow-right-circle, arrow-right, arrow-up-circle, arrow-up-left, arrow-up-right, arrow-up, at-sign, calendar, cast, check, chevron-down, chevron-left, chevron-right, chevron-up, chevrons-down, chevrons-left, chevrons-right, chevrons-up, circle, clipboard, clock, code, columns, command, compass, corner-down-left, corner-down-right, corner-left-down, corner-left-up, corner-right-down, corner-right-up, corner-up-left, corner-up-right, crosshair, database, divide-circle, divide-square, dollar-sign, download, external-link, feather, frown, hash, headphones, help-circle, info, italic, key, layout, life-buoy, link-2, link, loader, lock, log-in, log-out, maximize, meh, minimize, minimize-2, minus-circle, minus-square, minus, monitor, moon, more-horizontal, more-vertical, move, music, navigation-2, navigation, octagon, pause-circle, percent, plus-circle, plus-square, plus, power, radio, rss, search, server, share, shopping-bag, sidebar, smartphone, smile, square, table-2, tablet, target, terminal, trash-2, trash, triangle, tv, type, upload, x-circle, x-octagon, x-square, x, zoom-in, zoom-out

The MIT License (MIT) (for the icons listed above)

Copyright (c) 2013-present Cole Bemis

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

```
