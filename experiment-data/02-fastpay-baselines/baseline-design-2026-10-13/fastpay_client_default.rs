// Benchmark adapter for unmodified official FastPay libraries.
// It uses the safe normal transfer and receiver paths, never locally fabricated votes.
use fastpay::{config::*, network, transport};
use fastpay_core::{base_types::*, client::*, committee::Committee, error::FastPayError, messages::*};
use serde_json::json;
use std::{collections::HashMap, fs, sync::{Arc, Mutex}, time::{Duration, Instant}};
use structopt::StructOpt;

#[derive(StructOpt)]
struct Options {
    #[structopt(long)] dir: String,
    #[structopt(long)] prepare: bool,
    #[structopt(long, default_value="chain")] workload: String,
    #[structopt(long, default_value="100")] count: usize,
    #[structopt(long, default_value="100")] amount: u64,
    #[structopt(long, default_value="0")] rate: f64,
}

#[derive(Clone, Default)]
struct Clock(Arc<Mutex<Option<Instant>>>);
impl Clock {
    fn mark(&self) { self.0.lock().unwrap().get_or_insert_with(Instant::now); }
    fn reset(&self) { *self.0.lock().unwrap() = None; }
    fn elapsed_ms(&self) -> f64 { self.0.lock().unwrap().expect("no real RPC in payment").elapsed().as_secs_f64()*1000.0 }
}
#[derive(Clone)]
struct TimedClient { inner: network::Client, clock: Clock }
impl AuthorityClient for TimedClient {
    fn handle_transfer_order(&mut self, r: TransferOrder) -> AsyncResult<AccountInfoResponse, FastPayError> {
        self.clock.mark(); self.inner.handle_transfer_order(r)
    }
    fn handle_confirmation_order(&mut self, r: ConfirmationOrder) -> AsyncResult<AccountInfoResponse, FastPayError> {
        self.clock.mark(); self.inner.handle_confirmation_order(r)
    }
    fn handle_account_info_request(&mut self, r: AccountInfoRequest) -> AsyncResult<AccountInfoResponse, FastPayError> {
        self.clock.mark(); self.inner.handle_account_info_request(r)
    }
}
fn client(a: &UserAccount, c: &CommitteeConfig, clock: &Clock) -> ClientState<TimedClient> {
    let mut authorities = HashMap::new();
    for cfg in &c.authorities {
        authorities.insert(cfg.address, TimedClient { clock: clock.clone(), inner: network::Client::new(
            cfg.network_protocol, cfg.host.clone(), cfg.base_port, cfg.num_shards,
            transport::DEFAULT_MAX_DATAGRAM_SIZE.parse().unwrap(), Duration::from_secs(2), Duration::from_secs(2)) });
    }
    ClientState::new(a.address, a.key.copy(), Committee::new(c.voting_rights()), authorities,
        a.next_sequence_number, a.sent_certificates.clone(), a.received_certificates.clone(), a.balance)
}
fn prepare(o: &Options) -> Result<(), failure::Error> {
    let mut accounts = AccountsConfig::read_or_create(&format!("{}/accounts.json", o.dir))?;
    if accounts.num_accounts()!=0 { failure::bail!("prepare requires fresh accounts"); }
    let users = if o.workload=="chain" {3} else {2};
    let mut roles = Vec::new(); let mut initial = Vec::new();
    for i in 0..users {
        let funds = if i==0 {if o.workload=="chain" {o.amount} else {o.amount.checked_mul(o.count as u64).expect("principal overflow")}} else {0};
        let a = UserAccount::new(Balance::from(funds as i128));
        roles.push(encode_address(&a.address)); initial.push((a.address,a.balance)); accounts.insert(a);
    }
    accounts.write(&format!("{}/accounts.json",o.dir))?;
    InitialStateConfig{accounts:initial}.write(&format!("{}/initial_accounts.txt",o.dir))?;
    fs::write(format!("{}/roles.json",o.dir),serde_json::to_vec(&roles)?)?;
    Ok(())
}
async fn run(o: &Options) -> Result<(), failure::Error> {
    let c=CommitteeConfig::read(&format!("{}/committee.json",o.dir))?;
    if c.authorities.len()!=4 { failure::bail!("benchmark requires four real authorities"); }
    let a=AccountsConfig::read_or_create(&format!("{}/accounts.json",o.dir))?;
    let names:Vec<String>=serde_json::from_slice(&fs::read(format!("{}/roles.json",o.dir))?)?;
    let clock=Clock::default();
    let mut clients=Vec::new();
    for name in &names { let address=decode_address(name)?; clients.push(client(a.get(&address).ok_or_else(||failure::err_msg("missing role"))?,&c,&clock)); }
    let users=if o.workload=="chain" {3} else {2};
    if clients.len()!=users { failure::bail!("account count does not match requested workload"); }
    for (i,c) in clients.iter().enumerate() {
        let expected=if i==0 {if o.workload=="chain" {o.amount} else {o.amount.checked_mul(o.count as u64).expect("principal overflow")}} else {0};
        if c.balance()!=Balance::from(expected as i128) { failure::bail!("unexpected prefunding"); }
    }
    let mut samples=Vec::new(); let mut full_samples=Vec::new(); let mut build_samples=Vec::new();
    let mut chain_start=None; let mut last_endpoint=None; let workload_start=Instant::now();
    let mut sent_offsets=Vec::new(); let mut dispatch_lags=Vec::new(); let mut receipt_offsets=Vec::new();
    for i in 0..o.count {
        if o.rate>0.0 { tokio::time::delay_until(tokio::time::Instant::from_std(workload_start+Duration::from_secs_f64(i as f64/o.rate))).await; }
        let from=if o.workload=="chain" {i%3} else {0}; let to=if o.workload=="chain" {(i+1)%3} else {1};
        let recipient=clients[to].address();
        if o.workload=="chain" && clients[to].balance()!=Balance::zero() { failure::bail!("recipient has old principal instead of dependency"); }
        clock.reset(); let full_start=Instant::now();
        let cert=clients[from].transfer_to_fastpay(Amount::from(o.amount),recipient,UserData::default()).await?;
        let identity_matches=cert.value.transfer.sender==clients[from].address() && cert.value.transfer.recipient==Address::FastPay(recipient) && cert.value.transfer.amount==Amount::from(o.amount);
        clients[to].receive_from_fastpay(cert).await?;
        let endpoint=Instant::now(); let start=clock.0.lock().unwrap().expect("missing RPC timer");
        last_endpoint=Some(endpoint);
        if !identity_matches { failure::bail!("wrong certificate identity"); }
        let sent_offset=start.duration_since(workload_start).as_secs_f64()*1000.0;
        sent_offsets.push(sent_offset); receipt_offsets.push(endpoint.duration_since(workload_start).as_secs_f64()*1000.0);
        dispatch_lags.push(if o.rate>0.0 {sent_offset-i as f64*1000.0/o.rate} else {0.0});
        chain_start.get_or_insert(start);
        samples.push(endpoint.duration_since(start).as_secs_f64()*1000.0);
        full_samples.push(endpoint.duration_since(full_start).as_secs_f64()*1000.0);
        build_samples.push(start.duration_since(full_start).as_secs_f64()*1000.0);
        let sender_expected=if o.workload=="chain" {0} else {o.amount*(o.count-i-1) as u64};
        let receiver_expected=if o.workload=="chain" {o.amount} else {o.amount*(i+1) as u64};
        if clients[from].balance()!=Balance::from(sender_expected as i128) || clients[to].balance()!=Balance::from(receiver_expected as i128) { failure::bail!("balances do not conserve transferred principal"); }
    }
    let chain_ms=last_endpoint.unwrap().duration_since(chain_start.unwrap()).as_secs_f64()*1000.0;
    let realized_rate=if o.count>1 {Some((o.count-1) as f64*1000.0/(sent_offsets[o.count-1]-sent_offsets[0]))} else {None};
    // Prove the final recipient can actually spend; excluded from reported samples.
    let last=if o.workload=="chain" {o.count%3} else {1};
    let next=if o.workload=="chain" {(last+1)%3} else {0};
    let next_address=clients[next].address();
    let probe=clients[last].transfer_to_fastpay(Amount::from(o.amount),next_address,UserData::default()).await?;
    clients[next].receive_from_fastpay(probe).await?;
    let initial_principal=if o.workload=="chain" {o.amount} else {o.amount*o.count as u64};
    let sum=clients.iter().fold(Balance::zero(),|total,c|total.try_add(c.balance()).unwrap());
    if sum!=Balance::from(initial_principal as i128) { failure::bail!("final conservation failed"); }
    println!("{}",json!({"system":"FastPay","workload":o.workload,"completed":o.count,
        "samples_ms":samples,"request_to_receipt_ms":full_samples,"build_ms":build_samples,
        "chain_total_ms":chain_ms,"probe_completed":true,"principal_conserved":true,
        "sent_offset_ms":sent_offsets,"receipt_offset_ms":receipt_offsets,"dispatch_lag_ms":dispatch_lags,
        "realized_send_rate_per_s":realized_rate,"target_send_rate":o.rate,
        "receipt_goodput_per_s":o.count as f64*1000.0/chain_ms,
        "amount":o.amount,"authorities":4,"authority_storage":"memory","wallet_storage":"memory",
        "endpoint":"first authority RPC through normal safe transfer, certificate verification and receive_from_fastpay; no unsafe_unconfirmed API",
        "full_endpoint":"safe transfer API entry through receiver acceptance, includes construction/signing",
        "transport":format!("{:?}",c.authorities[0].network_protocol)}));
    Ok(())
}
fn main() -> Result<(), failure::Error> {
    let o=Options::from_args();
    if o.count==0 || o.amount==0 || !["chain","independent"].contains(&o.workload.as_str()) || !o.rate.is_finite() || o.rate<0.0 { failure::bail!("invalid workload arguments"); }
    if o.prepare { prepare(&o) } else { tokio::runtime::Runtime::new()?.block_on(run(&o)) }
}
#[cfg(test)] mod tests {
    use super::*;
    #[test] fn clock_starts_on_first_rpc_and_resets() {
        let c=Clock::default(); assert!(c.0.lock().unwrap().is_none()); c.mark();
        let first=c.0.lock().unwrap().unwrap(); std::thread::sleep(Duration::from_millis(1)); c.mark();
        assert_eq!(c.0.lock().unwrap().unwrap(),first); assert!(c.elapsed_ms()>=1.0);
        c.reset(); assert!(c.0.lock().unwrap().is_none());
    }
}
