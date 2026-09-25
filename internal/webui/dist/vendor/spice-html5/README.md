# spice-html5 (vendored)

- **Source:** https://gitlab.freedesktop.org/spice/spice-html5, a snapshot
  of the master branch from 2026-09-07.
- **Contents:** the files from `src/` and the `thirdparty/` files that
  `ticket.js` and `simulatecursor.js` import. `browser-es-module-loader`
  is not included.
- **Modifications:** none. The files are unmodified and not minified.
- **License:** LGPL-3.0-or-later. See `COPYING` and `COPYING.LESSER`. The
  `thirdparty/` files carry their own MIT-style and BSD license texts in
  their file headers.

nkt loads `main.js` with a dynamic import when a SPICE window opens.
Replace these files and rebuild `nkt` to use another version. See
THIRD_PARTY_NOTICES.md in the repository root.
