package doc_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
)

// Hermetic tests for the character-repertoire signal. No PDF and no poppler:
// every string is built here. The prose samples are ordinary appliance-manual
// sentences written in each language, because the signal reads the alphabet a
// page is actually typeset with and a contrived alphabet soup would prove
// nothing about real pages.

// markText builds text containing exactly the given characters at the given
// counts, spread through filler made only of letters no language in the table
// claims. It is how the measured per-column counts are reproduced exactly.
func markText(counts map[rune]int, filler string) string {
	runes := make([]rune, 0, len(counts))
	for r := range counts {
		runes = append(runes, r)
	}
	sort.Slice(runes, func(i, j int) bool { return runes[i] < runes[j] })

	var b strings.Builder
	for _, r := range runes {
		for range counts[r] {
			b.WriteString(filler)
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cyrillicFiller is shared Cyrillic prose: not one of its letters distinguishes
// any language from any other, so it sets the dominant script and nothing else.
const cyrillicFiller = " робот пилосос "

// The three columns of the measured page, at the counts measured on it:
//
//	column      Ukrainian marks   Russian marks   Kazakh marks
//	left                      0              40              0
//	middle                   83               0              0
//	right                    78             111            143
var (
	leftColumnMarks   = map[rune]int{'ы': 25, 'э': 10, 'ъ': 3, 'ё': 2}
	middleColumnMarks = map[rune]int{'і': 50, 'ї': 20, 'є': 10, 'ґ': 3}
	rightColumnMarks  = map[rune]int{
		'і': 78,
		'ы': 80, 'э': 25, 'ъ': 4, 'ё': 2,
		'ә': 25, 'ғ': 20, 'қ': 30, 'ң': 20, 'ө': 18, 'ұ': 15, 'ү': 10, 'һ': 5,
	}
)

func TestRepertoireSeparatesThreeCyrillicLanguagesOnOnePage(t *testing.T) {
	// This is what the signal exists for. ScriptRuns narrows a Cyrillic page to
	// seven candidates and stops; the measured fixture has a page whose three
	// columns are three different Cyrillic languages, one per column.
	cases := []struct {
		column string
		marks  map[rune]int
		want   string
	}{
		{"left", leftColumnMarks, "ru"},
		{"middle", middleColumnMarks, "uk"},
		{"right", rightColumnMarks, "kk"},
	}

	for _, c := range cases {
		text := markText(c.marks, cyrillicFiller)
		if s := doc.DominantScript(text); s != doc.ScriptCyrillic {
			t.Fatalf("%s column: script = %q, want Cyrillic", c.column, s)
		}

		m := doc.MatchRepertoire(text)
		got, ok := m.Language()
		if !ok || got != c.want {
			t.Errorf("%s column: got %q ok=%v, want %q\n%s", c.column, got, ok, c.want, m.Note)
		}
	}
}

func TestRepertoireReadsSharedCharactersAsSharedNotAsAVote(t *testing.T) {
	// The right column is why a maximum over per-language counts is the wrong
	// reading. Kazakh writes the і it shares with Ukrainian and the ы it shares
	// with Russian, so the overlapping counts the measurement recorded — 78
	// Ukrainian, 111 Russian, 143 Kazakh — are all Kazakh's, and the two rivals
	// are ruled out by what they *cannot* write rather than out-counted.
	m := doc.MatchRepertoire(markText(rightColumnMarks, cyrillicFiller))

	if m.Marks != 78+111+143 {
		t.Fatalf("marks = %d, want %d", m.Marks, 78+111+143)
	}

	byLang := make(map[string]doc.RepertoireCandidate, len(m.Candidates))
	for i := range m.Candidates {
		byLang[m.Candidates[i].Lang] = m.Candidates[i]
	}
	if _, admitted := byLang["ru"]; admitted {
		t.Error("Russian was admitted, though it cannot write і or any Kazakh letter")
	}
	if _, admitted := byLang["uk"]; admitted {
		t.Error("Ukrainian was admitted, though it cannot write ы or any Kazakh letter")
	}

	kk, ok := byLang["kk"]
	if !ok {
		t.Fatalf("Kazakh was not a candidate: %s", m.Note)
	}
	if kk.Matched != m.Marks || kk.Foreign != 0 {
		t.Errorf("Kazakh matched %d of %d with %d foreign, want all of them and none",
			kk.Matched, m.Marks, kk.Foreign)
	}
	// The whole point: the note has to let a human check the reading against the
	// page rather than take the score on trust.
	for _, want := range []string{"і×78", "ы×80", "ә×25"} {
		if !strings.Contains(m.Note, want) {
			t.Errorf("note does not show %s: %s", want, m.Note)
		}
	}
}

func TestRepertoireDeclinesTheWholeThreeColumnPage(t *testing.T) {
	// Read as one blob the same page has no answer, and saying so is the correct
	// behaviour: the characters of three languages fit none of them. This is what
	// makes the signal safe to run before column splitting rather than after.
	page := markText(leftColumnMarks, cyrillicFiller) +
		markText(middleColumnMarks, cyrillicFiller) +
		markText(rightColumnMarks, cyrillicFiller)

	m := doc.MatchRepertoire(page)
	if got, ok := m.Language(); ok {
		t.Errorf("named %q for a page of three languages: %s", got, m.Note)
	}
	if m.Marks == 0 {
		t.Error("reported no distinctive characters, though the page is full of them")
	}
	// Absent is not the same as blind: the note must still say what was found.
	if !strings.Contains(m.Note, "kk") {
		t.Errorf("note does not name the largest contributor: %s", m.Note)
	}
}

// manualProse is one ordinary paragraph of appliance-manual copy per language.
var manualProse = map[string]string{
	"ru": "Перед первым использованием робота-пылесоса внимательно прочитайте это руководство. " +
		"Не используйте устройство, если кабель повреждён. Съёмный контейнер для пыли следует " +
		"очищать после каждой уборки, а фильтр промывать тёплой водой без моющих средств. " +
		"Этот прибор не предназначен для использования детьми. Мыть щётку нельзя.",
	"uk": "Перед першим використанням робота-пилососа уважно прочитайте цей посібник. " +
		"Не використовуйте пристрій, якщо кабель пошкоджено. Знімний контейнер для пилу слід " +
		"очищати після кожного прибирання, а фільтр промивати теплою водою без мийних засобів. " +
		"Цей прилад не призначений для використання дітьми. Ґудзик живлення знаходиться збоку. " +
		"Якщо щітка забруднена, її потрібно зняти та промити. Є декілька режимів прибирання.",
	"kk": "Робот-шаңсорғышты алғаш рет пайдаланар алдында осы нұсқаулықты мұқият оқып шығыңыз. " +
		"Кабель зақымдалған болса, құрылғыны пайдаланбаңыз. Шаңға арналған алмалы контейнерді " +
		"әрбір тазалаудан кейін тазалап отырыңыз, ал сүзгіні жылы сумен жуыңыз. Бұл құрылғы " +
		"балалардың пайдалануына арналмаған.",
	"be": "Перад першым выкарыстаннем робата-пыласоса ўважліва прачытайце гэта кіраўніцтва. " +
		"Не выкарыстоўвайце прыладу, калі кабель пашкоджаны. Здымны кантэйнер для пылу трэба " +
		"чысціць пасля кожнай уборкі, а фільтр прамываць цёплай вадой. Гэты прыбор не " +
		"прызначаны для выкарыстання дзецьмі.",
	"bg": "Преди първата употреба на прахосмукачката робот прочетете внимателно това ръководство. " +
		"Не използвайте уреда, ако кабелът е повреден. Изваждащият се контейнер за прах трябва " +
		"да се почиства след всяко почистване, а филтърът да се измива с топла вода. Този уред " +
		"не е предназначен за употреба от деца. Съхранявайте ръководството.",
	"sr": "Пре прве употребе робота усисивача пажљиво прочитајте ово упутство. Немојте " +
		"користити уређај ако је кабл оштећен. Уклоњиви контејнер за прашину треба очистити " +
		"после сваког чишћења, а филтер испрати топлом водом. Овај уређај није намењен деци.",
	"mk": "Пред првата употреба на роботот правосмукалка внимателно прочитајте го ова упатство. " +
		"Не го користете уредот ако кабелот е оштетен. Подвижниот контејнер за прашина треба " +
		"да се исчисти по секое чистење, а филтерот да се измие со топла вода. Уредот ќе се " +
		"врати на базата автоматски. Не фрлајте ѓубре во контејнерот и не го ставајте до ѕидот.",

	"de": "Lesen Sie diese Anleitung vor der ersten Verwendung des Saugroboters sorgfältig durch. " +
		"Verwenden Sie das Gerät nicht, wenn das Kabel beschädigt ist. Der abnehmbare Staubbehälter " +
		"muss nach jeder Reinigung geleert werden, und der Filter ist mit warmem Wasser zu spülen. " +
		"Größere Fremdkörper müssen vorher entfernt werden. Öffnen Sie das Gehäuse nicht.",
	"fr": "Lisez attentivement ce manuel avant la première utilisation du robot aspirateur. " +
		"N'utilisez pas le robot si le câble est endommagé. Le bac à poussière amovible doit être " +
		"vidé après chaque nettoyage, et le filtre rincé à l'eau tiède. Ce produit n'est pas " +
		"destiné à être utilisé par des enfants. Vérifiez que la brosse est propre.",
	"es": "Lea atentamente este manual antes de utilizar el robot aspirador por primera vez. " +
		"No utilice el aparato si el cable está dañado. El depósito de polvo extraíble debe vaciarse " +
		"después de cada limpieza y el filtro debe lavarse con agua tibia. Este aparato no está " +
		"diseñado para ser utilizado por niños pequeños.",
	"it": "Leggere attentamente questo manuale prima di utilizzare il robot aspirapolvere. " +
		"Non utilizzare l'apparecchio se il cavo è danneggiato. Il contenitore della polvere " +
		"estraibile può essere svuotato dopo ogni pulizia, però il filtro va risciacquato con " +
		"acqua tiepida. Così l'apparecchio durerà più a lungo. Perché è necessario?",
	"pt": "Leia atentamente este manual antes da primeira utilização do robô aspirador. " +
		"Não utilize o aparelho se o cabo estiver danificado. O depósito de pó amovível deve ser " +
		"esvaziado após cada limpeza e o filtro lavado com água morna. Este aparelho não se destina " +
		"a ser utilizado por crianças. Verifique a posição da escova.",
	"pl": "Przed pierwszym użyciem robota odkurzającego należy uważnie przeczytać tę instrukcję. " +
		"Nie należy używać urządzenia, jeśli przewód jest uszkodzony. Wyjmowany pojemnik na kurz " +
		"należy opróżniać po każdym sprzątaniu, a filtr płukać ciepłą wodą. To urządzenie nie jest " +
		"przeznaczone do obsługi przez dzieci. Sprawdź, czy szczotka jest czysta.",
	"cs": "Před prvním použitím robotického vysavače si pečlivě přečtěte tento návod. " +
		"Nepoužívejte přístroj, pokud je kabel poškozený. Vyjímatelnou nádobu na prach je třeba " +
		"vyprázdnit po každém úklidu a filtr propláchnout vlažnou vodou. Tento přístroj není určen " +
		"pro použití dětmi. Zkontrolujte, zda je kartáč čistý.",
	"sk": "Pred prvým použitím robotického vysávača si pozorne prečítajte tento návod. " +
		"Nepoužívajte prístroj, ak je kábel poškodený. Vyberateľnú nádobu na prach je potrebné " +
		"vyprázdniť po každom upratovaní a filter prepláchnuť vlažnou vodou. Tento prístroj nie je " +
		"určený na používanie deťmi. Ľavá strana zariadenia musí byť voľná. Ôsmy krok je dôležitý.",
	"hu": "A robotporszívó első használata előtt figyelmesen olvassa el ezt az útmutatót. " +
		"Ne használja a készüléket, ha a kábel sérült. A kivehető portartályt minden takarítás után " +
		"ki kell üríteni, a szűrőt pedig langyos vízzel kell öblíteni. Ez a készülék nem gyermekek " +
		"általi használatra készült. Ellenőrizze a kefe állapotát.",
	"ro": "Citiți cu atenție acest manual înainte de prima utilizare a robotului aspirator. " +
		"Nu utilizați aparatul dacă cablul este deteriorat. Recipientul detașabil pentru praf " +
		"trebuie golit după fiecare curățare, iar filtrul trebuie clătit cu apă călduță. Acest " +
		"aparat nu este destinat utilizării de către copii. Verificați starea periei.",
	"tr": "Robot süpürgeyi ilk kez kullanmadan önce bu kılavuzu dikkatlice okuyun. " +
		"Kablo hasarlıysa cihazı kullanmayın. Çıkarılabilir toz haznesi her temizlikten sonra " +
		"boşaltılmalı ve filtre ılık suyla yıkanmalıdır. Bu cihaz çocuklar tarafından " +
		"kullanılmak üzere tasarlanmamıştır. Fırçanın temiz olduğunu kontrol edin.",
	"lt": "Prieš pirmą kartą naudodami robotą dulkių siurblį, atidžiai perskaitykite šį vadovą. " +
		"Nenaudokite prietaiso, jei laidas pažeistas. Išimamą dulkių talpyklą reikia ištuštinti " +
		"po kiekvieno valymo, o filtrą praplauti šiltu vandeniu. Šis prietaisas nėra skirtas " +
		"naudoti vaikams. Patikrinkite, ar šepetys švarus.",
	"lv": "Pirms putekļu sūcēja robota pirmās lietošanas rūpīgi izlasiet šo rokasgrāmatu. " +
		"Nelietojiet ierīci, ja kabelis ir bojāts. Izņemamā putekļu tvertne jāiztukšo pēc katras " +
		"tīrīšanas, bet filtrs jāizskalo ar siltu ūdeni. Šī ierīce nav paredzēta lietošanai " +
		"bērniem. Pārbaudiet, vai birste ir tīra.",
	"et": "Enne robottolmuimeja esmakordset kasutamist lugege see juhend hoolikalt läbi. " +
		"Ärge kasutage seadet, kui kaabel on kahjustatud. Eemaldatav tolmumahuti tuleb pärast iga " +
		"koristamist tühjendada ja filter loputada leige veega. Käesolev seade ei ole mõeldud " +
		"lastele kasutamiseks. Kontrollige, kas hari on puhas. Õhufilter tuleb vahetada.",
	"sl": "Pred prvo uporabo robotskega sesalnika natančno preberite ta priročnik. " +
		"Naprave ne uporabljajte, če je kabel poškodovan. Snemljivo posodo za prah je treba " +
		"izprazniti po vsakem čiščenju, filter pa sprati z mlačno vodo. Ta naprava ni namenjena " +
		"uporabi otrok. Preverite, ali je krtača čista.",
	"sv": "Läs denna bruksanvisning noggrant innan du använder robotdammsugaren för första gången. " +
		"Använd inte apparaten om kabeln är skadad. Den avtagbara dammbehållaren måste tömmas " +
		"efter varje rengöring och filtret sköljas i ljummet vatten. Den här apparaten är inte " +
		"avsedd att användas av barn. Kontrollera att borsten är ren.",
	"fi": "Lue tämä käyttöohje huolellisesti ennen robotti-imurin ensimmäistä käyttökertaa. " +
		"Älä käytä laitetta, jos johto on vaurioitunut. Irrotettava pölysäiliö on tyhjennettävä " +
		"jokaisen siivouksen jälkeen ja suodatin huuhdeltava haalealla vedellä. Tätä laitetta ei " +
		"ole tarkoitettu lasten käyttöön. Tarkista, että harja on puhdas.",
	"is": "Lesið þessar leiðbeiningar vandlega áður en ryksuguvélmennið er notað í fyrsta sinn. " +
		"Notið ekki tækið ef snúran er skemmd. Tæma þarf lausa rykhólfið eftir hverja þrif og " +
		"skola síuna með volgu vatni. Þetta tæki er ekki ætlað börnum. Athugið hvort burstinn sé " +
		"hreinn. Öll aukahlutir fylgja.",
}

// Prose that this signal must decline, and why.
var undetectableProse = map[string]string{
	"en": "Read this manual carefully before using the robot vacuum for the first time. " +
		"Do not use the appliance if the cable is damaged. The removable dust bin must be emptied " +
		"after every cleaning cycle and the filter rinsed in lukewarm water.",
	"id": "Bacalah petunjuk ini dengan saksama sebelum menggunakan robot penyedot debu untuk " +
		"pertama kali. Jangan gunakan perangkat jika kabel rusak. Wadah debu yang dapat dilepas " +
		"harus dikosongkan setelah setiap pembersihan dan filter dibilas dengan air hangat.",
	"ms": "Baca manual ini dengan teliti sebelum menggunakan robot penyedut habuk buat kali " +
		"pertama. Jangan gunakan perkakas jika kabel rosak. Bekas habuk yang boleh ditanggalkan " +
		"mesti dikosongkan selepas setiap pembersihan dan penapis dibilas dengan air suam.",
	"nl": "Lees deze handleiding zorgvuldig door voordat u de robotstofzuiger voor het eerst " +
		"gebruikt. Gebruik het apparaat niet als de kabel beschadigd is. Het uitneembare " +
		"stofreservoir moet na elke schoonmaakbeurt worden geleegd en het filter met lauw water " +
		"worden gespoeld.",
}

func TestRepertoireNamesTheLanguageOfOrdinaryProse(t *testing.T) {
	langs := make([]string, 0, len(manualProse))
	for lang := range manualProse {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	for _, want := range langs {
		m := doc.MatchRepertoire(manualProse[want])
		got, ok := m.Language()
		if !ok {
			t.Errorf("%s: declined to name a language: %s", want, m.Note)
			continue
		}
		if got != want {
			t.Errorf("%s: got %q — %s", want, got, m.Note)
		}
	}
}

func TestRepertoireSeparatesLatinLanguagePairs(t *testing.T) {
	// Pairs that a trigram detector confuses or that share most of an alphabet.
	// Czech and Slovak are here deliberately: they are usually listed together as
	// a hard pair, and by repertoire they are not, because Czech ř ě ů and Slovak
	// ľ ô ä are frequent enough in ordinary prose to contradict the other outright.
	pairs := [][2]string{
		{"cs", "sk"},
		{"es", "pt"},
		{"fi", "sv"},
		{"de", "et"},
		{"fr", "it"},
		{"lt", "lv"},
	}

	for _, pair := range pairs {
		for _, want := range pair {
			m := doc.MatchRepertoire(manualProse[want])
			got, ok := m.Language()
			if !ok || got != want {
				t.Errorf("%s vs %s: %s prose read as %q (ok=%v) — %s",
					pair[0], pair[1], want, got, ok, m.Note)
			}
		}
	}
}

func TestRepertoireRulesOutRivalsByWhatTheyCannotWrite(t *testing.T) {
	// A rival is not out-scored, it is contradicted. Czech is not admitted for
	// Slovak prose at all, because ľ and ô are letters Czech does not have.
	m := doc.MatchRepertoire(manualProse["sk"])
	for i := range m.Candidates {
		if m.Candidates[i].Lang == "cs" {
			t.Errorf("Czech was admitted for Slovak prose: %s", m.Note)
		}
	}
	if got, _ := m.Language(); got != "sk" {
		t.Fatalf("Slovak prose read as %q: %s", got, m.Note)
	}
}

func TestRepertoireKeepsASupersetLanguageAsARankedRunnerUp(t *testing.T) {
	// Russian's alphabet is contained in Kazakh's, so Kazakh explains a Russian
	// page perfectly and coverage alone cannot separate them. What separates them
	// is that none of Kazakh's own nine letters appear. Kazakh is ranked below
	// rather than discarded, because "it could be this" is a true statement and
	// the caller is entitled to see it.
	m := doc.MatchRepertoire(manualProse["ru"])

	got, ok := m.Language()
	if !ok || got != "ru" {
		t.Fatalf("Russian prose read as %q (ok=%v): %s", got, ok, m.Note)
	}

	var kk *doc.RepertoireCandidate
	for i := range m.Candidates {
		if m.Candidates[i].Lang == "kk" {
			kk = &m.Candidates[i]
		}
	}
	if kk == nil {
		t.Fatalf("Kazakh was dropped rather than ranked: %s", m.Note)
	}
	if kk.Foreign != 0 {
		t.Errorf("Kazakh reported %d foreign characters; it can write every Russian letter", kk.Foreign)
	}
	if kk.Score >= m.Candidates[0].Score {
		t.Errorf("Kazakh scored %.3f against Russian's %.3f", kk.Score, m.Candidates[0].Score)
	}
	if kk.Missing == "" {
		t.Error("Kazakh's absent letters were not reported, so the reason it lost is invisible")
	}
}

func TestRepertoirePrefersTheSmallerAlphabetThatFitsExactly(t *testing.T) {
	// Bulgarian has no exclusive letter at all: its alphabet is a strict subset of
	// Russian's. What identifies it is ъ in quantity while ы, э and ё never
	// appear, so Bulgarian must beat Russian on Bulgarian prose — and Russian must
	// still be listed, because on a short enough sample it would be the truth.
	m := doc.MatchRepertoire(manualProse["bg"])

	got, ok := m.Language()
	if !ok || got != "bg" {
		t.Fatalf("Bulgarian prose read as %q (ok=%v): %s", got, ok, m.Note)
	}
	found := false
	for i := range m.Candidates {
		if m.Candidates[i].Lang == "ru" {
			found = true
		}
	}
	if !found {
		t.Errorf("Russian was not offered as a runner-up: %s", m.Note)
	}
	if !strings.Contains(m.Note, "ыэё") {
		t.Errorf("note does not say which Russian letters are missing: %s", m.Note)
	}
}

func TestRepertoireReportsBlindSpotsAsTiesRatherThanPickingOne(t *testing.T) {
	// The pairs this signal provably cannot separate. Each must come back tied,
	// with Language() declining, and with every tied language named.
	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			"Danish and Norwegian",
			"Læs denne vejledning grundigt igennem, før du bruger robotstøvsugeren første gang. " +
				"Brug ikke apparatet, hvis kablet er beskadiget. Den aftagelige støvbeholder skal " +
				"tømmes efter hver rengøring. Åbn ikke kabinettet. Kontrollér, at børsten er ren.",
			[]string{"da", "no"},
		},
		{
			"Bosnian, Croatian and Serbian in Latin script",
			"Prije prve uporabe robotskog usisavača pažljivo pročitajte ovaj priručnik. " +
				"Nemojte koristiti uređaj ako je kabel oštećen. Odvojivi spremnik za prašinu treba " +
				"isprazniti nakon svakog čišćenja, a filtar isprati mlakom vodom.",
			[]string{"bs", "hr", "sr"},
		},
	}

	for _, c := range cases {
		m := doc.MatchRepertoire(c.text)
		if got, ok := m.Language(); ok {
			t.Errorf("%s: named %q instead of reporting a tie — %s", c.name, got, m.Note)
		}
		if !m.Ambiguous {
			t.Errorf("%s: not marked ambiguous — %s", c.name, m.Note)
		}
		tied := m.Tied()
		if len(tied) != len(c.want) {
			t.Errorf("%s: tied = %v, want %v", c.name, tied, c.want)
			continue
		}
		sort.Strings(tied)
		for i := range tied {
			if tied[i] != c.want[i] {
				t.Errorf("%s: tied = %v, want %v", c.name, tied, c.want)
				break
			}
		}
		for _, lang := range c.want {
			if !strings.Contains(m.Note, lang) {
				t.Errorf("%s: note does not name %s: %s", c.name, lang, m.Note)
			}
		}
	}
}

func TestRepertoireIsBlindToLanguagesWithoutDistinctiveCharacters(t *testing.T) {
	// English, Indonesian and Malay write nothing outside a-z, and Dutch writes
	// nothing it cannot do without. The signal must return no candidates at all
	// for them, rather than reaching for the nearest language that fits nothing.
	langs := make([]string, 0, len(undetectableProse))
	for lang := range undetectableProse {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	for _, lang := range langs {
		m := doc.MatchRepertoire(undetectableProse[lang])
		if got, ok := m.Language(); ok {
			t.Errorf("%s prose was named %q: %s", lang, got, m.Note)
		}
		if len(m.Candidates) != 0 {
			t.Errorf("%s prose produced %d candidates: %s", lang, len(m.Candidates), m.Note)
		}
		if m.Marks != 0 {
			t.Errorf("%s prose reported %d distinctive characters, want 0", lang, m.Marks)
		}
	}
}

func TestRepertoireTiesNamesTheIndistinguishableLanguages(t *testing.T) {
	cases := []struct {
		lang string
		want []string
	}{
		{"da", []string{"da", "no"}},
		{"no", []string{"da", "no"}},
		{"hr", []string{"bs", "hr", "sr"}},
		{"bs", []string{"bs", "hr", "sr"}},
		// Serbian is written in both scripts. Its Cyrillic form is unmistakable,
		// but the tie its Latin form is in still has to be reported.
		{"sr", []string{"bs", "hr", "sr"}},
		// No distinctive characters at all is the same statement about all three.
		{"id", []string{"en", "id", "ms"}},
		{"ms", []string{"en", "id", "ms"}},
		{"en", []string{"en", "id", "ms"}},
		// Separable, so each stands alone.
		{"cs", []string{"cs"}},
		{"sk", []string{"sk"}},
		{"ru", []string{"ru"}},
		{"uk", []string{"uk"}},
		// A language this signal knows nothing about at all.
		{"ja", nil},
	}

	for _, c := range cases {
		got := doc.RepertoireTies(c.lang)
		if len(got) != len(c.want) {
			t.Errorf("RepertoireTies(%q) = %v, want %v", c.lang, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("RepertoireTies(%q) = %v, want %v", c.lang, got, c.want)
				break
			}
		}
	}
}

func TestRepertoireSaysNothingWithoutEvidence(t *testing.T) {
	cases := []struct {
		name, text string
		wantMarks  int
	}{
		{"empty", "", 0},
		{"whitespace", "   \n\t  ", 0},
		{"digits and punctuation", "12.5 kg — 230 V / 50 Hz (±10%)", 0},
		{"a model number", "L40-U2400-B", 0},
		{"Greek, which script alone already settles", "Διαβάστε προσεκτικά αυτό το εγχειρίδιο.", 0},
		{"plain Latin prose", "Empty the dust bin after every cleaning cycle.", 0},
		// Two accented characters in an otherwise English caption are a brand
		// name, not a language.
		{"a brand name in English text", "Connect the Citroën adapter to the café socket.", 2},
	}

	for _, c := range cases {
		m := doc.MatchRepertoire(c.text)
		if got, ok := m.Language(); ok {
			t.Errorf("%s: named %q — %s", c.name, got, m.Note)
		}
		if len(m.Candidates) != 0 {
			t.Errorf("%s: produced %d candidates — %s", c.name, len(m.Candidates), m.Note)
		}
		if m.Marks != c.wantMarks {
			t.Errorf("%s: marks = %d, want %d", c.name, m.Marks, c.wantMarks)
		}
		if m.Note == "" {
			t.Errorf("%s: no note, so a caller cannot tell why nothing came back", c.name)
		}
	}
}

func TestRepertoireDeclinesTextMixingTwoLanguages(t *testing.T) {
	// Neither language can account for the other's characters, so neither is
	// admitted and the signal reports nothing — but the note names both, because
	// "these two are both here" is the useful part of the answer.
	m := doc.MatchRepertoire(manualProse["de"] + " " + manualProse["pl"])

	if got, ok := m.Language(); ok {
		t.Errorf("named %q for German mixed with Polish: %s", got, m.Note)
	}
	if len(m.Candidates) != 0 {
		t.Errorf("produced %d candidates: %s", len(m.Candidates), m.Note)
	}
	if m.Marks == 0 {
		t.Fatal("reported no distinctive characters for two languages full of them")
	}
	for _, lang := range []string{"de", "pl"} {
		if !strings.Contains(m.Note, lang) {
			t.Errorf("note does not name %s: %s", lang, m.Note)
		}
	}
}

func TestRepertoireForgivesASingleForeignCharacter(t *testing.T) {
	// A page of one language routinely carries a foreign name. One character it
	// cannot write must not rule the language out, and on a short paragraph a
	// percentage alone does exactly that: one stray in eleven marks is 9%.
	m := doc.MatchRepertoire(manualProse["de"] + " Zubehör von Nestlé.")

	got, ok := m.Language()
	if !ok || got != "de" {
		t.Errorf("German with one French accent read as %q (ok=%v): %s", got, ok, m.Note)
	}
}

func TestRepertoireReadsCedillaAndCommaAsTheSameLetter(t *testing.T) {
	// Romanian ș and ț are routinely typeset with a cedilla by fonts predating
	// Unicode 3.0. That is a typesetting choice, not a different language, and
	// both spellings of the same paragraph must reach the same answer.
	comma := manualProse["ro"]
	cedilla := strings.NewReplacer("ș", "ş", "ț", "ţ", "Ș", "Ş", "Ț", "Ţ").Replace(comma)
	if cedilla == comma {
		t.Fatal("the cedilla variant is identical to the comma variant; the test proves nothing")
	}

	want := doc.MatchRepertoire(comma)
	got := doc.MatchRepertoire(cedilla)

	wantLang, wantOK := want.Language()
	gotLang, gotOK := got.Language()
	if wantLang != "ro" || !wantOK {
		t.Fatalf("comma-below Romanian read as %q (ok=%v): %s", wantLang, wantOK, want.Note)
	}
	if gotLang != wantLang || gotOK != wantOK {
		t.Errorf("cedilla Romanian read as %q (ok=%v), comma-below as %q: %s",
			gotLang, gotOK, wantLang, got.Note)
	}
	if got.Marks != want.Marks {
		t.Errorf("cedilla spelling found %d marks, comma-below %d", got.Marks, want.Marks)
	}
}

func TestRepertoireIgnoresCharactersOfAnotherScript(t *testing.T) {
	// Cyrillic pages carry Latin furniture — model numbers, web addresses, brand
	// names. Counting those against the Cyrillic reading would let a product name
	// decide the language of the page it sits on.
	clean := manualProse["ru"]
	withLatin := clean + " Dreame L40 Ultra — Größe: 350 mm. Voir aussi: dreametech.com/support"

	before := doc.MatchRepertoire(clean)
	after := doc.MatchRepertoire(withLatin)

	if after.Script != doc.ScriptCyrillic {
		t.Fatalf("script = %q, want Cyrillic", after.Script)
	}
	if after.Marks != before.Marks {
		t.Errorf("Latin furniture changed the mark count from %d to %d", before.Marks, after.Marks)
	}
	got, ok := after.Language()
	if !ok || got != "ru" {
		t.Errorf("Russian with Latin furniture read as %q (ok=%v): %s", got, ok, after.Note)
	}
}

func TestRepertoireFoldsCase(t *testing.T) {
	// Headings and warning banners are set in capitals, and a page that is all
	// heading is exactly the short sample this signal is most needed for.
	lower := manualProse["hu"]
	upper := strings.ToUpper(lower)

	got := doc.MatchRepertoire(upper)
	want := doc.MatchRepertoire(lower)

	if got.Marks != want.Marks {
		t.Errorf("upper case found %d marks, lower case %d", got.Marks, want.Marks)
	}
	gotLang, gotOK := got.Language()
	if !gotOK || gotLang != "hu" {
		t.Errorf("upper-case Hungarian read as %q (ok=%v): %s", gotLang, gotOK, got.Note)
	}
}

func TestRepertoireMarksReportTheEvidenceItself(t *testing.T) {
	// The counts a caller can check by hand. Without these the score is an
	// assertion rather than a finding.
	counts := doc.RepertoireMarks(doc.ScriptCyrillic, markText(leftColumnMarks, cyrillicFiller))

	for r, want := range leftColumnMarks {
		if counts[r] != want {
			t.Errorf("%c counted %d, want %d", r, counts[r], want)
		}
	}
	if len(counts) != len(leftColumnMarks) {
		t.Errorf("counted %d distinct characters, want %d", len(counts), len(leftColumnMarks))
	}
	if doc.RepertoireMarks(doc.ScriptGreek, "Διαβάστε") != nil {
		t.Error("Greek has no repertoire table and must report no marks")
	}
}

func BenchmarkMatchRepertoire(b *testing.B) {
	// One manual page is ~1700 characters (docs/design/ingest.md), so the sample
	// is padded to that length: the cost that matters is per page, over 560 of them.
	page := strings.Repeat(manualProse["ru"], 1700/len([]rune(manualProse["ru"]))+1)
	b.ReportAllocs()
	for b.Loop() {
		doc.MatchRepertoire(page)
	}
}
