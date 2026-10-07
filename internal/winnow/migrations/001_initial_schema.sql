-- events holds each Event that one or more Digests match, one row for each
-- Event. The JSON column holds the Event without Body and Push.Commits.
CREATE TABLE events (
    id          INTEGER PRIMARY KEY,
    received_at INTEGER NOT NULL, -- Unix seconds
    delivery    TEXT UNIQUE,      -- NULL when the delivery has no ID
    repo        TEXT NOT NULL,
    name        TEXT NOT NULL,    -- the Event name
    event       TEXT NOT NULL
);
CREATE INDEX events_received_at ON events (received_at);
CREATE INDEX events_repo ON events (repo);
CREATE INDEX events_name ON events (name);

-- digest_events holds the names of the Digests that matched each stored
-- Event when winnow received it. A Digest counts only its own Events, so a
-- new Digest or a wider match does not count older Events that another
-- Digest stored.
CREATE TABLE digest_events (
    digest TEXT NOT NULL,
    event  INTEGER NOT NULL REFERENCES events (id),
    PRIMARY KEY (digest, event)
);

-- digests holds the first-run time of each Digest, by "<name>/<every>". A
-- Period that ended before it does not exist for that Digest.
CREATE TABLE digests (
    name      TEXT PRIMARY KEY,
    first_run INTEGER NOT NULL -- Unix seconds
);

-- periods holds the send state of each Period of each Digest. A Period with
-- no row has no state.
CREATE TABLE periods (
    digest TEXT NOT NULL, -- "<name>/<every>"
    period TEXT NOT NULL, -- the first day, YYYY-MM-DD
    state  TEXT NOT NULL CHECK (state IN ('sent', 'failed', 'skipped', 'empty')),
    PRIMARY KEY (digest, period)
);
