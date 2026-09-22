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
