# The wordmark's script face

The wordmark — *Clip Manager* — is written rather than set, the whole name in
one hand. (SAND Vault's lockup mixes a mono acronym with a script word because
SAND is an acronym; Clip is just a word, so both words here wear the same
face.) This is where that face lives.

## What ships here

`wordmark-script.woff2` — **5.8 KB**, the glyphs of `Clip Manager` and nothing
else. It is a subset of [**Caveat**](https://fonts.google.com/specimen/Caveat)
by Pablo Impallari, used under the SIL Open Font License 1.1, whose full text
is in `OFL.txt` beside it. A monoline hand with a large x-height and short
ascenders — which is why it stays legible at the 17px the header sets it in,
where a finer copperplate turns to mud.

Caveat ships as a variable font. The copy here is pinned to **weight 500**: one
weight is all the wordmark sets, and carrying the axis would mean carrying
every master behind it.

The name is kept. Caveat reserves none — its copyright line names no Reserved
Font Name — so the subset stays honest about whose drawing it is, with the
copyright, designer and licence records preserved in the name table.

## How it is used

The build (see `web/vite.config.js`) reads the file off disk and embeds it in
`index.html` as a `data:` URI. Nothing is ever fetched from a CDN: the app
makes zero third-party requests, and the wordmark is not the thing to break
that for. With the file absent the build says so and the wordmark falls back
to the platform's own handwriting face — see `FONT.script` in
`web/src/theme.js`.

## Rebuilding it

```bash
pip install 'fonttools[woff]' brotli
curl -O https://raw.githubusercontent.com/google/fonts/main/ofl/caveat/Caveat%5Bwght%5D.ttf
python scripts/make-wordmark-font.py 'Caveat[wght].ttf' --weight 500
```

The source font is not kept in this repository — download it from upstream and
point the script at it. `--family` renames the output, which is required if the
source is ever swapped for a face that reserves its name.
