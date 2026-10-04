from pathlib import Path
from collections import Counter
import re, json
n = Path(__file__).resolve().parent
p = n.parent
def text(name, old=False):
    return ((n/'before' if old else p)/name).read_text(encoding='utf-8')
def envs(s, env):
    return Counter(re.sub(r'\s+', '', x) for x in re.findall(r'\\begin\{'+re.escape(env)+r'\}.*?\\end\{'+re.escape(env)+r'\}',s,re.S))
report=[]
for prefix in ['', 'zh-']:
    old = '\n'.join(text(prefix+x+'.tex',True) for x in ['model','protocol','security','evaluation','network-current'])
    new = '\n'.join(text(prefix+x+'.tex') for x in ['model','protocol','history','security','evaluation','network-current'])
    for env in ['equation','align','align*','lemma','theorem','IEEEproof','algorithmic']:
        assert envs(old,env)==envs(new,env),(prefix,env,'changed or lost')
        report.append({'language':prefix or 'en','protected_environment':env,'count':sum(envs(new,env).values()),'unchanged':True})
    for stem in ['evaluation','network-current']:
        a,b=text(prefix+stem+'.tex',True),text(prefix+stem+'.tex')
        assert envs(a,'tabular')==envs(b,'tabular'),(prefix,stem,'table changed')
    oldlabels=set(re.findall(r'\\label\{([^}]+)\}',old))
    newlabels=set(re.findall(r'\\label\{([^}]+)\}',new))
    assert oldlabels<=newlabels,oldlabels-newlabels
for source in (n/'before').glob('*supplement*.tex'):
    assert source.read_bytes()==(p/source.name).read_bytes(),source.name
(n/'content-preservation.json').write_text(json.dumps(report,indent=2),encoding='utf-8')
print('Protected formulas, lemmas, theorems, proofs, algorithms, evaluation tables, and supplement files preserved.')
