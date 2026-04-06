#!/usr/bin/env python3
import sys, requests, json
from bs4 import BeautifulSoup

def search(query, max_results=5):
    headers = {"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36"}
    r = requests.get(f"https://html.duckduckgo.com/html/?q={requests.utils.quote(query)}", 
                     headers=headers, timeout=10)
    soup = BeautifulSoup(r.text, "html.parser")
    results = []
    for a in soup.select(".result__title a")[:max_results]:
        snippet = a.find_next(".result__snippet")
        results.append({
            "title": a.get_text(strip=True),
            "snippet": snippet.get_text(strip=True) if snippet else ""
        })
    print(json.dumps(results))

search(sys.argv[1] if len(sys.argv) > 1 else "test")
