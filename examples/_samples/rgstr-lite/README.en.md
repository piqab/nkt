# rgstr-lite — a tutorial plugin

The plugin the “Writing a plugin step by step” guide article builds: a summary
with findings, repositories and tags, delete a tag, collect garbage, a robot
for a pipeline (the secret once to the window or into a pipeline: `.env` or
the registry key). For real work use **rgstr** from the hub catalog.

Upload: Plugins → Catalog → Upload your own plugin → the `rgstr-lite.tar.gz`
archive (the “Download the sample” button or
`tar czf rgstr-lite.tar.gz -C examples/_samples rgstr-lite`).

Settings: `RGSTR_URL`, `RGSTR_USER`, `RGSTR_PASSWORD` (secret),
`RGSTR_PUBLIC` — as for the rgstr plugin. Script command:

```
rgstr-lite gc
rgstr-lite robot ci project shop to-pipeline shop env @registry
```
