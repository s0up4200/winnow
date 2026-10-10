**Version**
v1.87.0

**Describe the bug**
When "Release Name" and "Hash" are used for "Exact Release" duplicate profile, the SQL query still uses just the Hash without comparing Release Names resulting in it thinking that the provided release is a duplicate due to multiple previous releases of completely different episodes having the same normalized hash.

In this case for Episode 23 of "Ascendance of the Bookworm: Adopted Daughter of an Archduke" on AnimeBytes Indexer with the SubsPlease release:
```
14:12:55 DBG check is duplicate with profile filter=AB-SubsPlease method=CheckFilter module=filter profile="Exact release" release="[SubsPlease] Ascendance of a Bookworm: Adopted Daughter of an Archduke - 23 [2026][Web][MKV][h264][1080p][AAC 2.0][Softsubs (SubsPlease)]" trace_id=darrdhli3kdg00fgd790
14:12:55 TRC check duplicate release args=["c13d4175c211a993cdfbff1eb06b2b25"] database=release.FindDuplicateReleases query="SELECT r.id, r.torrent_name, r.normalized_hash, r.title, ras.action, ras.status FROM release r LEFT JOIN release_action_status ras ON r.id = ras.release_id WHERE ras.status = 'PUSH_APPROVED' AND r.normalized_hash = $1" repo=release
14:12:55 TRC found duplicate releases database=release.FindDuplicateReleases releases=[{},{},{},{},{},{},{},{},{},{},{},{},{},{},{},{},{},{},{},{}] repo=release
```

Running this statement on the database results in multiple rows of previous episodes as shown here (cut off, but it goes up to Episode 22 and does not include Episode 1 and Episode 20 as they have a different hash):

<img width="2553" height="730" alt="Image" src="https://github.com/user-attachments/assets/b0330735-324f-4bdc-b322-c0896cf913f2" />

Episode 23 that is being checked here is not present in this list and should be accepted due to a different `torrent_name` (I assume this is checked as "Release Name"), but instead it gets rejected as a duplicate.

I have previously not used duplicate profiles so that is why I have not noticed this as of yet and why there's so many rows already.

**To Reproduce**
Steps to reproduce the behavior:
1. Get an earlier episode of the same series through the AB indexer (not 1 and 20 as they have different hashes).
2. Try to get a different episode like 23.
3. The episode gets rejected as a duplicate even though it is not, but the hashes match for some reason.

**Expected behavior**
The episode should not be rejected as a duplicate

**Desktop (please complete the following information):**
N/A as the issue is server side in the latest (v1.87.0) container image.
