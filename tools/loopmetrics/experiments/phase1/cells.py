import sys,glob,os,collections
sys.path.insert(0,os.path.join(os.path.dirname(os.path.abspath(__file__)),'..','..'))
import loopmetrics as lm
R=os.environ.get('LM_RUNS','runs')+'/'
for cond in ('chain','compact','interrupt','unverif'):
    for arm in 'AME':
        cell=f'{arm}-{cond}'
        specs=[]
        for rep in range(1,6):
            fs=sorted(glob.glob(f'{R}{cell}/r{rep}-t*.jsonl'))
            if fs: specs.append(','.join(fs))
        s=lm.cell_summary(cell,specs)
        print(cell,'runs',s['runs'],'failed',len(s['failed']),s['verdict'])
        for k in ('m1.rate','m1.max_run','m2.within_median','m2.cross_max','m2.cross_share@0.8','m2.within_share@0.5','m4.max_run','reasoning_chars','requests','cost'):
            v=s['series'][k]; f=lambda x:'-' if x is None else (f'{x:.3f}' if isinstance(x,float) else x)
            print(f'   {k:<20} med {f(v["median"])} min {f(v["min"])} max {f(v["max"])} n={v["n"]}')
