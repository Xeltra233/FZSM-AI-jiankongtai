#!/usr/bin/env python3
import json,re,subprocess,sys
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]

def tracked():
    raw=subprocess.check_output(['git','ls-files','-z'],cwd=ROOT)
    return [x.decode('utf-8') for x in raw.split(b'\0') if x]

def main():
    files=tracked(); findings=[]
    forbidden=re.compile(r'(^|/)(\.env($|\.)|auth/cookies(?:\.backup\..*)?\.json$)|\.(db|db-wal|db-shm|pem|p12|pfx)$',re.I)
    secret_patterns=[
      ('aws_access_key',re.compile(r'AKIA[0-9A-Z]{16}')),
      ('github_token',re.compile(r'(?:ghp_|github_pat_)[A-Za-z0-9_]{20,}')),
      ('openai_key',re.compile(r'sk-[A-Za-z0-9_-]{20,}')),
      ('bearer_token',re.compile(r'Bearer\s+[A-Za-z0-9._~-]{24,}',re.I)),
    ]
    for rel in files:
      p=ROOT/rel
      if forbidden.search(rel): findings.append({'severity':'high','type':'tracked_sensitive_file','path':rel})
      try:
        if p.stat().st_size>50*1024*1024: findings.append({'severity':'high','type':'tracked_large_file','path':rel,'size':p.stat().st_size})
        b=p.read_bytes()
      except OSError: continue
      if b'\0' in b[:4096]: continue
      text=b.decode('utf-8','ignore')
      for name,rx in secret_patterns:
        if rx.search(text): findings.append({'severity':'high','type':name,'path':rel})
      if rel in {'docker-compose.yml','Dockerfile','scripts/start-server.sh'} and re.search(r'(?i)(changeme|password123|admin123)',text):
        findings.append({'severity':'high','type':'weak_default_password','path':rel})
    out={'tracked_files':len(files),'findings':findings,'ok':not findings}
    print(json.dumps(out,ensure_ascii=False,indent=2))
    return 0 if out['ok'] else 1
if __name__=='__main__': raise SystemExit(main())
