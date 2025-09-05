# Recently done

[✅] implement SSL certificate
[✅] implement "onhold" status for workflow template (so that the workflow starts only when it is ready)
[✅] implement task_dependancies (SQL table exists but that's it for now)
[✅] add local worker recruitment
  [✅] Ensure "local" provider and region exist
  [✅] Add a new gRPC endpoint: RegisterSpecifications(ResourceSpec)
  [✅] Implement server-side logic for RegisterSpecifications
  [✅] Implement client-side logic for RegisterSpecifications
  [✅] Test recruitment and worker visibility
[✅] add proper version in code, report in UI
[✅] add reporting of error of client (called worker events) in the server, accessible by CLI
[✅] add OVH support
[✅] check task retries

# TODO short term


[ ] show worker event in UI
[ ] step view stats in UI


# TODO later

[ ] implement some timeouts for workflow template scripts
[ ] implement debug mode
[ ] implement workflow strategy (sticky)
[ ] add run duration measurement
[ ] add helper as `source /resource/shell_helpers.sh` (or `source /builtinresource/shell_helpers.sh` ?), rather than copying it in all scripts, same for find_pairs, (`source /builtinresource/shell_biology.sh`)