# Test fonts

`ElMessiri.ttf` is **El Messiri** (variable font, default instance Regular),
Copyright 2015 The El Messiri Project Authors
(https://github.com/Gue3bara/El-Messiri), licensed under the SIL Open Font
License 1.1 — see `OFL.txt`. Downloaded from
https://github.com/google/fonts/tree/main/ofl/elmessiri.

It is the one binary fixture the tests commit: rendering text deterministically
across machines needs a known font, and El Messiri covers both Arabic and Latin
(needed for the mixed Arabic/English bidi tests; Noto Sans Arabic has no Latin
glyphs). The e2e tests use it by file, and by family name ("El Messiri") through
a fontconfig configuration that points at this directory.
