# CLI REST endpoint

Livepeer node exposes REST endpoint which is used by `livepeer_cli` for communication with node.
By default it is exposed on port 7935.

## Exposed endpoints:


### `/logLevel` - gets or sets current verbosity level of log output

`/logLevel` returns currrent verbosity level in the body of response

`/logLevel?level=9` will set verbosity level to 9


