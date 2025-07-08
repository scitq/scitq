# TODO short term

[✅] implement SSL certificate
[✅] implement "onhold" status for workflow template (so that the workflow starts only when it is ready)
[✅] implement task_dependancies (SQL table exists but that's it for now)
[✅] add local worker recruitment
  [✅] Ensure "local" provider and region exist
  [✅] Add a new gRPC endpoint: RegisterSpecifications(ResourceSpec)
  [✅] Implement server-side logic for RegisterSpecifications
  [✅] Implement client-side logic for RegisterSpecifications
  [✅] Test recruitment and worker visibility

# TODO later

[ ] implement some timeouts for workflow template scripts
[ ] implement debug mode
[ ] implement workflow strategy (sticky)
[ ] add OVH support
[ ] add run duration measurement