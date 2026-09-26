package deck

import "github.com/scuba-plaza/arabic-vocab/internal/anki"

const (
	ModelID      int64 = 1758873600101
	DeckID       int64 = 1758873600202
	DeckName           = "Arabic::MSA Core"
	FontFile           = "_ScheherazadeNew-Regular.ttf"
	ModelName          = "Arabic MSA Word"
	DeckFileName       = "arabic-msa-core"
)

var Fields = []string{
	"Arabic", "English", "Hint", "Forms", "Example", "ExampleEnglish",
	"WordAudio", "FormsAudio", "ExampleAudio", "Pos", "Details", "Production",
	"Check", "Position", "NoteID", "Source",
}

func fieldIndex(name string) int {
	for i, f := range Fields {
		if f == name {
			return i
		}
	}
	panic("unknown field " + name)
}

const panels = `{{#Forms}}
<div class="panel forms">
  <div class="forms-row">{{Forms}}</div>
  <div class="play">{{FormsAudio}}</div>
</div>
{{/Forms}}
{{#Example}}
<div class="panel example">
  <div class="ar sentence" lang="ar" dir="rtl">{{Example}}</div>
  <div class="en">{{ExampleEnglish}}</div>
  <div class="play">{{ExampleAudio}}</div>
</div>
{{/Example}}
{{#Check}}<div class="check">{{Check}}</div>{{/Check}}`

const recognitionFront = `<div class="side">
  <div class="ar headword" lang="ar" dir="rtl">{{Arabic}}</div>
  <div class="play">{{WordAudio}}</div>
</div>`

const recognitionBack = `{{FrontSide}}
<hr id="answer">
<div class="meaning">{{English}}</div>
{{#Hint}}<div class="hint">{{Hint}}</div>{{/Hint}}
<div class="meta">{{Pos}}{{#Details}} · {{Details}}{{/Details}}</div>
` + panels

const productionFront = `{{#Production}}
<div class="side">
  <div class="meaning prompt">{{English}}</div>
  {{#Hint}}<div class="hint">{{Hint}}</div>{{/Hint}}
  <div class="meta">{{Pos}}</div>
  {{#ExampleEnglish}}<div class="context">{{ExampleEnglish}}</div>{{/ExampleEnglish}}
</div>
{{/Production}}`

const productionBack = `{{FrontSide}}
<hr id="answer">
<div class="ar headword" lang="ar" dir="rtl">{{Arabic}}</div>
<div class="play">{{WordAudio}}</div>
<div class="meta">{{Pos}}{{#Details}} · {{Details}}{{/Details}}</div>
` + panels

const css = `@font-face {
  font-family: "Scheherazade New";
  src: url("_ScheherazadeNew-Regular.ttf");
}

.card {
  font-family: "Liberation Sans", "Helvetica Neue", Arial, sans-serif;
  font-size: 20px;
  text-align: center;
  line-height: 1.4;
  color: #1d1d1f;
  background-color: #fafafa;
}

.card.nightMode, .nightMode .card, .night_mode .card {
  color: #ececec;
  background-color: #1e1e1e;
}

.ar {
  font-family: "Scheherazade New", serif;
  direction: rtl;
  line-height: 1.9;
}

.headword {
  font-size: 2.8rem;
  margin: 0.3rem 0 0.1rem;
}

.meaning {
  font-size: 1.9rem;
  margin: 0.5rem 0 0.2rem;
}

.prompt {
  font-size: 2.2rem;
}

.hint, .meta, .context {
  color: #8a8a8e;
}

.hint {
  font-size: 1rem;
  margin-bottom: 0.2rem;
}

.hint .ar, .context .ar {
  font-size: 1.3rem;
}

.meta {
  font-size: 0.85rem;
  letter-spacing: 0.02em;
}

.context {
  font-size: 1rem;
  font-style: italic;
  margin-top: 0.8rem;
}

.panel {
  margin: 1rem auto;
  padding: 0.7rem 1.1rem;
  border-radius: 16px;
  max-width: 92%;
  border: 1px solid rgba(255, 255, 255, 0.06);
  box-shadow: 0 4px 15px rgba(0, 0, 0, 0.35);
  color: #ffffff;
}

.forms {
  background-color: #1a2a33;
}

.example {
  background-color: #1a3026;
}

.forms-row {
  font-size: 1rem;
}

.form {
  display: inline-block;
  margin: 0 0.5rem;
  white-space: nowrap;
}

.form .ar {
  font-size: 1.6rem;
}

.lbl {
  font-size: 0.8rem;
  opacity: 0.7;
  margin-right: 0.3rem;
}

.sentence {
  font-size: 1.8rem;
}

.sentence b {
  color: #ffd479;
}

.panel .en {
  font-size: 1rem;
  opacity: 0.85;
}

.play {
  margin-top: 0.2rem;
}

.check {
  margin: 1rem auto;
  max-width: 92%;
  padding: 0.6rem 0.9rem;
  border-radius: 12px;
  background-color: #4a3a10;
  color: #ffe7a3;
  font-size: 0.9rem;
  text-align: left;
}

.check .ar {
  font-size: 1.3rem;
}

.replay-button svg {
  width: 30px;
  height: 30px;
}
`

func NoteType() anki.Model {
	return anki.Model{
		ID:     ModelID,
		Name:   ModelName,
		Fields: Fields,
		RTL:    []string{"Arabic", "Example"},
		Templates: []anki.Template{
			{Name: "Recognition", Front: recognitionFront, Back: recognitionBack},
			{Name: "Production", Front: productionFront, Back: productionBack},
		},
		CSS: css,
		Required: [][]int{
			{fieldIndex("Arabic")},
			{fieldIndex("Production"), fieldIndex("English")},
		},
	}
}
