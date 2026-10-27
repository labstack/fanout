# Landing clips

The landing page's chat and dashboard slots play short clips of the real
Fanout UI. They are produced outside this repository from screen captures of a
Fanout instance with chat enabled; answers, panels and numbers are as recorded.

Expected assets, each at 16:10:

- `chat-{light,dark}.{webm,mp4,png}`
- `dashboard-{light,dark}.{webm,mp4,png}`

Keep each video at about 4 MB. The PNG poster is the clip's final frame.

Videos are never committed. Published videos live in the Cloudflare R2 bucket
`fanout` under `videos/`, served at `https://assets.fanout.run/videos/`. The
bucket holds public assets only, in one folder per file type. PNG posters stay
in `site/src/assets/landing/` so Astro can optimize them.

Each slot stays poster-only until both themes have both video formats in
`site/src/data/landing-media.json`. An absent manifest also shows posters. The
component follows the site theme, pauses off screen and in hidden tabs, and
shows only the poster when reduced motion is enabled. Its `spedUp` prop marks
clips with shortened waits in the caption.

Copy the reviewed posters into `site/src/assets/landing/`, then upload the
videos from the repository root with Wrangler logged in to the account that
owns `fanout`:

```sh
bash site/scripts/upload-clips.sh /path/to/clips
```

The uploader writes content-addressed immutable keys, skips unchanged clips
already listed in the manifest, and preserves URLs for clips absent from the
input directory. It never deletes old objects because published pages may still
reference them. After uploading, commit `site/src/data/landing-media.json` and
the PNG posters.
