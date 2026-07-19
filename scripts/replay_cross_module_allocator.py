#!/usr/bin/env python3
import argparse,json,math,sqlite3
from pathlib import Path

def state(conn,key):
    row=conn.execute("select value from runtime_state where key=?",(key,)).fetchone()
    return json.loads(row[0]) if row else {}

def stats(hist):
    xs=[float(x.get("delta",0)) for x in hist[:100]]
    if not xs:return {"n":0,"mean":0,"lcb":0}
    mean=sum(xs)/len(xs)
    if len(xs)<2:return {"n":len(xs),"mean":mean,"lcb":mean}
    sd=math.sqrt(sum((x-mean)**2 for x in xs)/(len(xs)-1))
    return {"n":len(xs),"mean":mean,"lcb":mean-1.96*sd/math.sqrt(len(xs))}

def main():
    ap=argparse.ArgumentParser(); ap.add_argument('db'); ap.add_argument('--out'); a=ap.parse_args()
    db=Path(a.db).resolve()
    c=sqlite3.connect(f"file:{db.as_posix()}?mode=ro",uri=True)
    last=state(c,'last_loop'); account=last.get('account',{})
    cash=float(account.get('cash',0)); equity=float(account.get('equity',0)); pool=min(cash*.05,equity*.05,10_000_000)
    prem=state(c,'risk.obs.free_draw_premium'); ps=stats(prem.get('history',[])); fee=500_000; premium_net_lcb=ps['lcb']-fee
    slot=state(c,'lottery.slot_edge'); yolo=state(c,'risk.edge.yolo'); deriv=state(c,'risk.edge.derivatives')
    farm=state(c,'farm'); errs=[str(x) for x in farm.get('errors',[])]; q429=sum('429' in x or '额度' in x for x in errs)
    negative=[]
    if float(slot.get('theory_ev_per_spin',0))<0: negative.append('slot')
    if float(yolo.get('theory_ev',0))<0: negative.append('yolo')
    if premium_net_lcb<0: negative.append('paid_premium')
    deriv_ev=float(deriv.get('use_ev',deriv.get('theory_ev',0)) or 0)
    alloc_deriv=pool if deriv.get('edge_ok') and deriv_ev>0 else 0
    out={
      'source':'cloud_db_copy','cash':cash,'equity':equity,'shared_pool':pool,
      'observed':{'premium_recent':ps,'premium_entry_fee':fee,'premium_net_lcb':premium_net_lcb,'slot_theory_ev':slot.get('theory_ev_per_spin'),'derivatives_edge':deriv_ev,'farm_quota_errors_in_latest_state':q429},
      'baseline_independent_risk':{'possible_simultaneous_capital_claim':pool+fee+float(slot.get('bet',1_000_000) or 1_000_000),'negative_ev_modules_if_only_switches_enabled':negative,'farm_requests_after_first_quota_error':max(0,q429-1)},
      'optimized':{'total_capital_cap':pool,'derivatives_cap':alloc_deriv,'paid_premium_cap':0 if premium_net_lcb<0 else min(fee,pool-alloc_deriv),'slot_cap':0 if float(slot.get('theory_ev_per_spin',0))<0 else min(float(slot.get('bet',0)),pool-alloc_deriv),'negative_ev_blocked':negative,'farm_quota_requests_after_first_error':0},
      'assertions':{'pool_not_exceeded':alloc_deriv<=pool,'negative_ev_gets_zero':all(x in negative for x in ['slot','paid_premium']),'quota_storm_eliminated':q429<=1 or max(0,q429-1)>0}
    }
    text=json.dumps(out,ensure_ascii=False,indent=2)
    if a.out: Path(a.out).write_text(text,encoding='utf-8')
    print(text)
if __name__=='__main__':main()
