import sys

from camel_tools.disambig.mle import MLEDisambiguator
from camel_tools.utils.dediac import dediac_ar


def arabic_word(w):
    return w and all('ء' <= c <= 'ي' for c in w)


def read_types(spec):
    path, _, limit = spec.partition(':')
    limit = int(limit) if limit else 0
    out = []
    with open(path, encoding='utf-8') as f:
        for line in f:
            parts = line.replace('\t', ' ').split()
            if len(parts) != 2:
                continue
            w = dediac_ar(parts[0]).replace('\u0640', '')
            if arabic_word(w):
                out.append(w)
                if limit and len(out) >= limit:
                    break
    return out


def main():
    if len(sys.argv) < 3:
        sys.exit('usage: camel_lemmas.py OUT.tsv FREQ_FILE[:LIMIT] ...')
    words = []
    seen = set()
    for spec in sys.argv[2:]:
        for w in read_types(spec):
            if w not in seen:
                seen.add(w)
                words.append(w)
    mle = MLEDisambiguator.pretrained()
    with open(sys.argv[1], 'w', encoding='utf-8') as out:
        for i in range(0, len(words), 2000):
            chunk = words[i:i + 2000]
            for w, d in zip(chunk, mle.disambiguate(chunk)):
                if not d.analyses:
                    continue
                a = d.analyses[0].analysis
                lex = a.get('lex', '')
                if not lex or lex == 'NOAN':
                    continue
                gloss = (a.get('stemgloss') or '').replace('\t', ' ')
                out.write(f"{w}\t{lex}\t{a.get('pos', '')}\t{gloss}\n")
            print(f'{min(i + 2000, len(words))}/{len(words)}', file=sys.stderr)


if __name__ == '__main__':
    main()
