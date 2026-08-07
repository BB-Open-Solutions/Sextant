# DPIA (concept) - Sextant fleet management

**Status: CONCEPT, geschreven door de bouwers, 7 augustus 2026.**

Dit is geen vastgestelde DPIA. Het is de voorbereiding die de
verwerkingsverantwoordelijke en de FG normaal zelf moeten samenstellen uit
gesprekken met de leverancier: welke gegevens, langs welke weg, met welke
maatregelen, en wat er overblijft. Dat deel is hier al gedaan en gemeten.

Wat de FG moet doen is beoordelen, aanvullen en vaststellen. Wat hier staat
kan feitelijk fout zijn geworden sinds de meetdatum; elke bewering draagt
daarom waar hij vandaan komt.

Grondslag voor het uitvoeren: art. 35 AVG. Deze verwerking is niet
onbetwistbaar DPIA-plichtig, maar P8 hieronder (journaalfragmenten van
werkplekken van medewerkers) is voldoende reden om er een te doen in plaats
van te beargumenteren waarom niet.

## 1. Beschrijving van de verwerking

Sextant beheert de configuratie van werkplekken. Het bepaalt wat er op een
apparaat draait, controleert of het apparaat dat ook echt doet, en biedt een
console waarin beheerders dat zien en bijsturen.

**Rollen.** De gemeente is verwerkingsverantwoordelijke. Wie de console
draait is verwerker. Sextant zelf is software en geen partij. Zie
[processing-register.md](processing-register.md) voor de uitwerking; een
verwerkersovereenkomst is vereist en ontbreekt op dit moment
([dpa-draft.md](dpa-draft.md)).

**Betrokkenen.** Medewerkers met een beheerde werkplek, en de ICT-beheerders
die de console gebruiken.

**Gegevens.** Tien verwerkingen, uitgewerkt in het register. Samengevat:
toegewezen gebruiker per apparaat, identiteit en groepslidmaatschap van
beheerders, verhogingsverzoeken met vrije tekst, check-ins met technische
toestand, en diagnostiekbundels.

**Niet verwerkt**, en dat is een ontwerpkeuze die vastligt: geen
toetsaanslagen, geen schermbeelden, geen browsegeschiedenis, geen locatie,
geen applicatiegebruik per gebruiker, en geen interactieve overname op
afstand (`docs/capabilities.md:45` legt die weigering vast).

## 2. Noodzaak en evenredigheid

De gemeente moet werkplekken beheren: dat volgt uit haar zorgplicht voor
informatiebeveiliging (BIO) en uit gewoon werkgeverschap. De vraag is niet
of, maar hoeveel.

Twee dingen die in het voordeel wegen en die feitelijk controleerbaar zijn:

- **De configuratie is data, geen scripts.** Wat een apparaat doet staat in
  een leesbaar document in git. Een betrokkene kan in beginsel zien wat er
  voor zijn apparaat geldt, en een beheerder kan niets doen wat niet in dat
  document staat.
- **Het model is pull.** Apparaten halen hun configuratie op; de console
  duwt niets uit. Dat begrenst wat er ongemerkt kan gebeuren.

Waar het schuurt is P8, hieronder.

## 3. Risico's voor betrokkenen

Geordend naar wat het voor een medewerker betekent, niet naar technische
ernst.

### R-A. Een diagnostiekbundel bevat meer dan de storing (hoog)

Een bundel draagt journaalfragmenten van de machine van een medewerker.
Journalen zijn niet selectief: er kunnen bestandspaden, gebruikersnamen,
hostnamen van dingen waarmee iemand verbond, en foutmeldingen met
willekeurige inhoud in staan.

**Kans:** laag - een bundel wordt per apparaat opgevraagd, niet doorlopend
verzameld. **Gevolg:** hoog - het is de meest onthullende gegevensverzameling
in het product.

**Maatregelen die bestaan en gemeten zijn:** versleuteld bij opslag; alleen
op verzoek; automatisch weg na 14 dagen, afgedwongen bij élke uitlezing en
niet door een opruimtaak die kan uitvallen.

**Restrisico, en dit is het scherpste punt van deze DPIA: de gebruiker wordt
niet verteld dat er een bundel is opgehaald.** Dat is transparantie
(art. 13/14), geen beveiliging. Het is een keuze die de gemeente moet maken -
óf het apparaat meldt het, óf de eigen privacyverklaring dekt het - en het is
nu geen van beide.

### R-B. De beheerder ziet meer dan nodig (midden)

Rolgebaseerde toegang bestaat en wordt per verzoek opnieuw bepaald uit de
directory, met bereik per organisatie, groep of apparaat. Een beheerder met
org-bereik ziet echter alles.

**Restrisico:** dit is een organisatorische keuze, geen technisch gebrek. De
gemeente moet bepalen wie org-bereik krijgt en dat periodiek herzien. Het
product kan dat niet voor haar beslissen.

### R-C. Gegevens die blijven staan (was hoog, nu laag)

Tot 7 augustus 2026 had één van tien verwerkingen een bewaartermijn. De rest
groeide onbegrensd - een verhogingsverzoek van twee jaar oud, met naam en
wat iemand wilde doen, had geen doel meer.

**Maatregel, gebouwd op 7 augustus:** termijnen op notificaties (180 dagen),
verhogingsverzoeken (365), beheerdersidentiteiten (365) en check-ins (180,
en alleen voor apparaten die de vloot niet meer kent).

**Restrisico:** de termijnen zijn **standaardwaarden van de leverancier**.
De verwerkingsverantwoordelijke moet ze bevestigen of wijzigen; dat besluit
is nog niet genomen.

### R-D. Het auditspoor is onuitwisbaar (aanvaard, moet vastgelegd)

Elke configuratiewijziging is een git-commit met auteur en tijd. Dat is
vereist vanuit de BIO en is precies wat art. 17 vraagt te kunnen verwijderen.

Er is sinds 7 augustus een wisroute (art. 17) die de console-gegevens van één
persoon verwijdert en **altijd rapporteert wat níet verwijderd kon worden**.
De git-historie staat daar altijd bij.

**Restrisico:** de gemeente moet schriftelijk vastleggen dat
verantwoordingsgegevens een eigen grondslag en bewaartermijn hebben. Zonder
dat is "we kunnen de historie niet wissen" een mededeling in plaats van een
onderbouwing.

### R-E. Verlies van herstelsleutels (laag, maar onherstelbaar)

De console bewaart de LUKS-herstelsleutel van elk apparaat. Het apparaat wist
zijn eigen kopie zodra de console bevestigt. Verlies betekent dat een
medewerker niet meer bij zijn versleutelde schijf komt.

**Maatregelen:** versleuteld bij opslag; sinds 7 augustus een backup buiten
het cluster, en het herstelpad is dezelfde dag daadwerkelijk uitgevoerd en
geverifieerd. Bevestiging aan het apparaat gebeurt alleen als het opslaan
echt slaagde - dat was tot 7 augustus niet getest.

**Restrisico:** de versleutelingssleutel zelf zit niet in de backup. Wie die
kwijtraakt houdt onleesbare gegevens over. Sleutelbeheer is een eigen
maatregel en nog niet belegd.

### R-F. Wachtwoorden van medewerkers over het netwerk (hoog, in behandeling)

Aanmelden op een apparaat gaat via een simple bind, en die draagt het
wachtwoord zelf. De console gebruikt sinds 7 augustus ldaps met strikte
verificatie. **De apparaten nog niet**, en zolang poort 389 open staat is
versleuteling niet afgedwongen maar vrijwillig.

**Restrisico: dit is het enige openstaande punt met een hoge ernst dat
betrokkenen direct raakt.** Zie auditbevinding H3.

## 4. Wat vóór ingebruikname geregeld moet zijn

1. **Verwerkersovereenkomst** tussen gemeente en beheerder van de console.
2. **Besluit over transparantie bij R-A**: meldt het apparaat het, of dekt
   de privacyverklaring het?
3. **Bevestiging van de bewaartermijnen** door de verwerkingsverantwoordelijke.
4. **Schriftelijke onderbouwing bij R-D** voor het auditspoor.
5. **Afronding van R-F**: apparaten naar ldaps, daarna afdwingen.
6. **Sleutelbeheer voor de versleutelingssleutel** (R-E).

## 5. Wat dit document niet is

Het is niet vastgesteld, niet juridisch getoetst en niet ondertekend. Het is
geschreven door de mensen die weten waar de gegevens staan, zodat het gesprek
met de FG begint bij "klopt dit" in plaats van bij een leeg blad.

Meetdatum van alle feitelijke beweringen: 7 augustus 2026. Wie dit later
leest moet de metingen opnieuw doen; het register en de audits in
`docs/audit/` beschrijven hoe.
