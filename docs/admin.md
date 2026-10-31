# Admins

Admins are listed by sign-in identity in `CUPSTREAM_ADMINS` (`discord:<user ID>`, `google:<sub>`; `dev:<handle>` with the development login), never by handle, which users can change. They get a shield icon in the header that leads to `/admin`, which shows:

- every live stream with its room, viewers, and the bitrate it receives and sends, plus totals
- an **End** button for each stream (the kill switch)
- platform bans

Banning a user:

- revokes all their sessions
- ends their streams (end reason `user_banned`)
- closes their viewer sessions and WebSockets
- refuses their sign-in, streaming and OBS key

Linking or merging a banned account into another is refused too. Admins can't be banned; take them out of `CUPSTREAM_ADMINS` first. Signed-out watching of rooms open to everyone can't be prevented. Bans and ended streams are logged with the admin's user ID.
