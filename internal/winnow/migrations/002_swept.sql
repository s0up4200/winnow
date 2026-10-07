-- swept holds the GUID of each missed delivery that a Sweep handled. When a
-- Source has a Sweep, it also holds the GUID of each live delivery. The
-- primary key is the claim: only the Sweep or the live delivery that
-- inserts the row handles the delivery. Each Sweep deletes the rows older
-- than 4 days.
CREATE TABLE swept (
    guid     TEXT PRIMARY KEY,
    swept_at INTEGER NOT NULL -- Unix seconds
);
