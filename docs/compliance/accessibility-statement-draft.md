# Toegankelijkheidsverklaring (concept)

**Status: CONCEPT, 7 augustus 2026. Niet gepubliceerd.**

Een toegankelijkheidsverklaring publiceer je in het officiële register
(toegankelijkheidsverklaring.nl) met een voorgeschreven structuur. Dit is de
inhoud, ingevuld met wat gemeten is, zodat wie hem publiceert niet hoeft te
verzinnen wat er waar is.

**Hij mag nog niet de deur uit.** De reden staat in §3: de handmatige ronde
is niet gedaan, en een verklaring die meer claimt dan getoetst is, is een
onjuiste verklaring. Dat is erger dan een verklaring die "gedeeltelijk"
zegt - dat laatste is normaal en verwacht.

## 1. Nalevingsstatus

**Voldoet gedeeltelijk** - er is een quickscan uitgevoerd, geen volledig
onderzoek.

Wie dit publiceert moet dat waarmaken met een onderzoeksrapport. Dit
document is de basis daarvoor, geen vervanging.

## 2. Wat onderzocht is, en hoe

Statische analyse van alle sjablonen van de console op 7 augustus 2026,
vastgelegd in [accessibility-audit.md](accessibility-audit.md). De controles
zitten sindsdien in de testsuite
(`internal/http/web/a11y_test.go`), dus ze verlopen niet stilzwijgend: een
nieuw formulierveld zonder toegankelijke naam laat de build vallen op de
commit die het introduceert.

**Gevonden en opgelost, dezelfde dag:**

| | gevonden | nu |
|---|---|---|
| Formuliervelden zonder toegankelijke naam (WCAG 3.3.2, 4.1.2) | 73 van 146 | 0 |
| Knoppen met alleen een icoon, zonder naam (4.1.2) | 11 van 101 | 0 |
| Skip-link naar de hoofdinhoud (2.4.1) | ontbrak | aanwezig |
| Taal van de pagina (3.1.1) | inlogpagina stond vast op Engels | volgt de locale |

**Wat al goed was:** elke afbeelding heeft een alt-tekst, elke pagina heeft
precies één `h1`, en de kernschermen werken zonder JavaScript.

## 3. Wat NIET onderzocht is

Dit is de belangrijkste paragraaf van dit concept. Een machine vindt de
helft die mechanisch is; de andere helft vraagt een mens.

Nog te doen vóór publicatie:

1. **Toetsenbordronde** over de vijf kernschermen (overzicht, apparaten,
   instellingen, wijzigingen, uitrol): volgorde, zichtbare focus, geen vallen.
2. **Schermlezer** (NVDA of VoiceOver) op diezelfde vijf. Hier blijkt het
   verschil tussen "technisch gelabeld" en "bruikbaar".
3. **Contrast** in beide thema's, gemeten in plaats van bekeken.
4. **Herschaling tot 400%** (1.4.10). De instellingentabellen zijn het risico.
5. **Foutherkenning** (3.3.1): zegt een geweigerd formulier wát er mis was,
   waar, en in tekst in plaats van alleen met kleur?
6. **De nieuwe 2.2-criteria**: focus niet afgedekt (2.4.11), sleepbewegingen
   (2.5.7), consistente hulp (3.2.6), herhaalde invoer (3.3.7).

## 4. Verbeterpunten en planning

De mechanische bevindingen zijn dicht. De handmatige ronde is niet ingepland
en heeft geen uitvoerder. Dat is het eerlijke antwoord op de vraag "wat is de
planning": er is er nog geen, en dat moet er komen vóór september.

## 5. Feedback

De gemeente vult hier haar eigen contactgegevens in, plus de
escalatieprocedure als een melding niet naar tevredenheid wordt afgehandeld.
Dat hoort bij de organisatie die de console gebruikt, niet bij de leverancier.

## 6. Aanvullende informatie van de leverancier

- **Waarom de basis goed is:** de console is server-gerenderde HTML zonder
  JavaScript-vereiste voor de kernschermen. Dat is de gunstigst mogelijke
  uitgangspositie, en de reden dat het resterende werk mechanisch is in
  plaats van architectonisch.
- **Waarom het toch misging:** het ontwerp koos NL Design System juist om
  deze reden, en daarna heeft niemand gemeten. De les zit nu in de
  testsuite in plaats van in een voornemen.
- Bron van elke bewering hierboven: `docs/compliance/accessibility-audit.md`,
  met meetdatum.
