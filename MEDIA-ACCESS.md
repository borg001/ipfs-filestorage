# Media delivery authorization

Configure `MEDIA_ACCESS_URL` with the application's CID policy endpoint and
`MEDIA_LINK_ACCESS_URL` with its placement endpoint. The IAMFree endpoints are
`/api/media_delivery` and `/internal/api/media_delivery_link`. The latter must
be reachable from Storage; it is not a browser API.

Deploy the matching API and local upload-preview UI changes before this Storage
version. The API explicitly authorizes saved chat/support attachments and
registered unplaced uploads for their owner. Before registration, the form
previews the selected local bytes; unknown CIDs are never made public to support
that preview. An empty
policy, inaccessible placement, or unknown resource now returns **403**; a
failed policy dependency returns **503**. Neither result allows original bytes.

An absent or invalid policy configuration also fails closed. A standalone
installation that intentionally has no per-file ACLs must explicitly set
`MEDIA_ALLOW_UNMANAGED=true` and leave both policy URLs empty. Do not enable
that mode on a protected IAMFree deployment.

The policy response binds a placement to `storage_uri`, `poster_uri` and a
delivery mode. A browser cannot apply a public placement to a different CID.
Legacy HLS playlists carry `media_root`; Storage validates their children
against the immutable master/variant playlists. A client-supplied
`metadata.stream_cids` list is not proof of membership. Opaque indexed HLS URLs
remain supported and avoid scanning the compatibility tree.

Both `blur` and `blur_faces` prohibit original video streams, including direct
segments and requests disguised with an image extension. Only an available
protected poster is delivered. Missing protected renditions fail closed.

Protected responses use `Cache-Control: private, no-store`. Check reverse proxy
cache rules on deployment: an old response previously marked public is not
retroactively invalidated by these headers. Invalidate any affected shared
cache entries if such a cache was enabled.

This change fixes authorization using online policy decisions. It does not
implement signed download grants or remove the API policy call per download.

The stand check additionally covered actual private-photo bytes, verification
owner/agency/staff, protected video posters, HLS master/variant/init/media segments,
and direct-CID substitution. Denials also carry private/no-store headers.
Before deployment, audit legacy raw avatar references as described by the API
change: references to another profile's restricted library do not grant access.

## What `fix/media-rate-limit` adds on top

These changes were made on the IAMFree stand and merged with the contract
above (fail closed, placements bound to `storage_uri`/`poster_uri`).

- **A `media_link` speaks only for its own file.** When the policy answer for
  the link names another source than the CID in the path, the link is set
  aside and the file is judged by its CID alone (`mediaAccessResolver.Resolve`).
- **Deny is a refusal, not an outage.** A `deny` row, an empty answer and
  401/403/404 from the policy all answer **403** (`errMediaForbidden` is the
  same error as `errMediaAccessDenied`); only a failed dependency is 503.
- **Video with a hidden face.** `blur` and `blur_faces` both refuse the master,
  playlists and segments; the refusal says *Private video is unavailable* or
  *Video with a hidden face is unavailable* (`mediaDeliveryHidesStream`). The
  protected poster the master binds to the mode stands in for the video.
- **Redrawn face masks.** A mask is drawn at upload and kept in the bundle,
  whose address cannot change. `POST /admin/face-masks/refresh` (platform API
  key only) redraws masks of photo bundles and video posters from the
  originals and records each replacement in `/data/variant-overrides.json`
  on the instance's volume; `overriddenFile` serves the replacement in place
  of the bundled mask, falling back to the bundled one if it cannot be read.
  The override is applied to the poster the master binds, never to one named
  by editable metadata.
- **Uploads.** HEIC stills are converted to JPEG at ingest; EXIF (with GPS),
  XMP, IPTC and comments are stripped from JPEG/PNG/WebP originals (a JPEG
  keeps its orientation); a session uploads at most
  `UPLOAD_DAILY_BYTES_PER_SESSION` a day per instance (4 GiB by default); a
  multi-file upload carries at most 20 files; a picture over 100 megapixels is
  refused before it is decoded; videos wait for one of two transcoding slots
  (five minutes at most), a transcode stops after twenty minutes, frames over
  4096 px a side or 120 fps are refused; a video of any shape is accepted and
  its poster is scaled inside the variant box.
- **Stored files never act as pages.** A CID is a CID and nothing after it;
  stored files are served with a sandboxing CSP and `nosniff`, HTML and SVG
  uploads are refused, and `DELETE /file` takes the platform API key only. The
  session may come in the `iamfree_media_token` cookie instead of the query.
- **Bursts.** The rate limit allows the bursts of media-heavy pages.

### Stand configuration after the merge

`MEDIA_ACCESS_URL` names the API's `/api/media_delivery` (it answers chat and
support attachments too, and the unregistered pictures a record names: an
agency's or a manager's avatar from «About me» and a tour's cover).
`MEDIA_LINK_ACCESS_URL` stays `/internal/api/media_delivery_link`. Before the
merge this storage read the API branch's own `/api/media_delivery_cid`, which
answered an unplaced file with no rows; the merged storage refuses that, and
the API has removed the endpoint.

The policy read takes about 0.1 s once the API's database connection sends
`jit=off`; with JIT, compiling it took about 1.8 s, close to
`MEDIA_ACCESS_TIMEOUT_MS` (2500), and avatars answered 503.
