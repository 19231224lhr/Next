import reproduce as r
r.build()
r.run_case("v3-diagnostic10",10,"fast",True)
for repeat in range(1,4):
 for length in [1,10,100]:
  for mode in (["fast","wait_final"] if repeat%2 else ["wait_final","fast"]):
   r.run_case(f"v3-{mode}-{length}-r{repeat}",length,mode)
