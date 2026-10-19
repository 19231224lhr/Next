# C3 proof map (working notes; not a completed formal proof)

The source design is authoritative. These notes separate executable checks from the arguments still needed in the manuscript. Do not turn a passing experiment into a proof claim.

## F1: coverage of hidden certificates

For an authorization pool of cumulative funded amount B, each correct member may retain at most floor(2B_m/3), where its verified B_m<=B. A local approval counts M minus only publicly authenticated applied unused release U. Partial approvals count too. Spent FUEL remains counted.

With one faulty member among four, any three-signature certificate has at least two correct approvers. Summing retained liabilities over the three correct members bounds twice the liability of all valid certificate facts by 3 floor(2B/3)<=2B. Hence their total spent+held liability is at most B. Correct members that have not observed a release retain more, not less. With all four members correct, each certificate has three correct approvers; the bound is 4 floor(2B/3)/3<=8B/9<=B.

To complete this argument for the implementation, establish: a unique fact across alternative quorum subsets; immutable M/pool/grant; persistent atomic reservations before vote; input conflicts and partial-approval invalidation preserve counting; no unverified release; all grants sharing one real pool counted without duplicating funding; top-up increases B only by a real transfer. Configuration transitions and wallet migration retain old authorizations. Availability is separate: partial approvals can consume capacity without completing certificates.

## F2: fixed user net fee

For the same successfully executed payment set and actual payers, each first execution consumes authenticated FUEL inputs and returns change equal to input total minus the signed quote q, or debits the explicit sponsor by q. Repeated executions are no-ops. The q credit goes once to the issuer operating account. No source-open/compensation/repayment operation accesses the downstream user's fee funds. Summing these transfers yields net fee per payer equal to the sum of accepted quotes, independently of public execution order.

Implementation obligations: every fee input belongs to the bound payer and is authorized; sponsor authority and quota verified; rejected transactions leave no fee residue; quote is not collected at certificate issuance; alternate commitment/payer mutation cannot authenticate. Delayed source that never executes is outside the all-payments-executed F2 comparison and has not yet paid its quote.

## F3: remaining funds and once-only stages

Induct over atomic public state transitions. Each first attached fact debits exactly M from its authorized protected pool. Its fee state partitions M into burned+rewards+held+released. Each stage converts held into charge or unused release and persists a stable completed-stage marker. Replays leave every counter and balance unchanged. Compensation retains not-yet-paid source execution reward and each compensated output's repay cost. Execute resolves all direct outputs before releasing their unused stages.

Task1 pure-state tests enumerate legal two-output paths and verify this partition, stage order and reward ceilings. Integration must show the balance transfers, CAL transitions, state record and event markers commit together; a rejected enclosing operation rolls back all of them. Restart/repair must preserve records and dedup identity. H>=MaxFutureCost is meaningful only for a validated state; it does not assert progress without the underlying protocol's liveness conditions.

## F4: fixed service reward

Attach and execute reward flags are once-only. Each output has one resolution reward shared by source fulfillment and reserve compensation; repayment adds none. Every open/close/compensate/repay charge goes to burned accounting. Thus rewards never exceed the authenticated plan, even if execution order changes. Bind the beneficiary as well as amounts. This removes direct additional event-reward income, not every possible collusion incentive.

## Whole-system accounting

Initial real FUEL plus explicit external funded transfers equals all current FUEL UTXOs/account balances plus real escrow held plus cumulative burned. Rewards already in beneficiary accounts must not be added a second time. M, grant amounts, local reservations and Released are accounting quantities, not additional assets. A CAL repayment never restores a spent FUEL amount. Service revenue minus protocol costs is not net economic profit after operating/capital costs.

Final evidence map must name actual test cases and measured runs after they execute; no placeholders count as verification.
