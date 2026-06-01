# guise docs site

The project website ([jjshanks.github.io/guise](https://jjshanks.github.io/guise/)),
built with [Hugo](https://gohugo.io/) and the
[hugo-book](https://github.com/alex-shpak/hugo-book) theme.

The theme is pulled in as a **Hugo Module** (see `go.mod`) — there is no git
submodule to init. `SPEC.md` and `docs/logo.png` from the repo root are mounted
straight into the site (see `[module.mounts]` in `hugo.toml`), so the spec stays
the single source of truth with no copy step.

## Local development

Requires the **extended** Hugo (the theme compiles SCSS) and Go (for modules):

```powershell
# Live-reload server at http://localhost:1313/guise/
hugo server

# One-off production build into ./public
hugo --gc --minify
```

If `hugo version` doesn't say `+extended`, grab the extended build from the
[Hugo releases](https://github.com/gohugoio/hugo/releases).

## Deployment

Pushing to `main` triggers `.github/workflows/pages.yml`, which builds the site
and publishes it to GitHub Pages. Set **Settings → Pages → Source** to
**GitHub Actions** once.
