import contextlib
import json
import sys

from camel_tools.disambig.bert import BERTUnfactoredDisambiguator
from camel_tools.morphology.analyzer import Analyzer
from camel_tools.morphology.database import MorphologyDB
from camel_tools.utils.dediac import dediac_ar
from catt_tashkeel import CATTEncoderOnly


def check(items):
    analyzer = Analyzer(MorphologyDB.builtin_db())
    bert = BERTUnfactoredDisambiguator.pretrained()
    catt = CATTEncoderOnly()

    bare = [[dediac_ar(t).replace('ـ', '') for t in item['tokens']] for item in items]
    contextual = [i for i, item in enumerate(items) if item.get('context') and bare[i]]
    catt_out = {}
    if contextual:
        vocalized = catt.do_tashkeel_batch([' '.join(bare[i]) for i in contextual], verbose=False)
        catt_out = dict(zip(contextual, vocalized))

    cache = {}
    results = []
    for i, (item, words) in enumerate(zip(items, bare)):
        analyses = []
        for w in words:
            if w not in cache:
                cache[w] = sorted({a['diac'] for a in analyzer.analyze(w) if a.get('diac')})
            analyses.append(cache[w])
        catt_tokens = bert_tokens = None
        if i in catt_out:
            catt_tokens = catt_out[i].split()
            if len(catt_tokens) != len(words):
                catt_tokens = None
            bert_tokens = [d.analyses[0].analysis.get('diac', '') if d.analyses else '' for d in bert.disambiguate(words)]
        results.append({
            'id': item['id'],
            'field': item['field'],
            'analyses': analyses,
            'catt': catt_tokens,
            'bert': bert_tokens,
        })
    return results


def main():
    items = [json.loads(line) for line in sys.stdin if line.strip()]
    out = sys.stdout
    with contextlib.redirect_stdout(sys.stderr):
        results = check(items)
    for r in results:
        out.write(json.dumps(r, ensure_ascii=False) + '\n')


if __name__ == '__main__':
    main()
