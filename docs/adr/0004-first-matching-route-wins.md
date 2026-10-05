# The first matching Route wins

Routes are one ordered list. Winnow checks them in file order. The first Route that matches an Event decides what happens to it: send to its Sinks, or drop. Winnow checks no more Routes. One Route can name several Sinks.

If all matching Routes fire, a broad Route such as `repo: autobrr/*` also sends security events to its public channel. Each broad Route then needs an exception for the security events, and one forgotten exception leaks a secret. With first match, the security Route goes before the broad Routes.

## Considered options

- All matching Routes fire. Rejected because of the leak above.
- A separate global drop list that winnow checks before the Routes. Rejected because a `drop` Route in the ordered list does the same with one mechanism.

## Consequences

The order of Routes in the file is their priority. A broad Route placed too early hides the Routes below it. Fan-out to several channels is a list of Sinks on one Route, not several matching Routes.
