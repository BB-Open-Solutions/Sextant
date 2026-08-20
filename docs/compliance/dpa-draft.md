# Verwerkersovereenkomst (concept)

**Status: CONCEPT, 7 augustus 2026. Niet juridisch getoetst, niet getekend.**

Een verwerkersovereenkomst is verplicht (art. 28 lid 3 AVG) en ontbreekt op
dit moment. Dit concept vult de technische delen in met wat er echt is - de
maatregelen, de subverwerkers, de bewaartermijnen - zodat de jurist over de
juridische delen gaat in plaats van over de feiten.

**Wat hier feitelijk staat is gemeten op 7 augustus 2026.** Wie dit later
gebruikt moet die metingen herhalen; `docs/audit/` beschrijft hoe.

## Partijen

- **Verwerkingsverantwoordelijke:** de gemeente.
- **Verwerker:** de partij die de Sextant-console beheert.

Sextant is software en geen partij. Beheert de gemeente de console zelf, dan
vallen beide rollen samen en is deze overeenkomst niet nodig - maar de
maatregelen in bijlage B blijven dan wél de eigen verantwoordelijkheid.

## 1. Onderwerp en duur

De verwerker beheert de werkplekconfiguratie van de gemeente en de daarbij
horende gegevens, zoals beschreven in bijlage A. De overeenkomst loopt zolang
de dienst loopt en eindigt met de verwijdering of teruggave uit artikel 8.

## 2. Instructies

De verwerker verwerkt uitsluitend op schriftelijke instructie van de
verantwoordelijke. Deze overeenkomst en de configuratie in het vlootdocument
gelden als zodanig.

**Wat dit concreet betekent, en het is ongebruikelijk genoeg om te noemen:**
de instructie is leesbaar en versiebeheerd. Wat de verwerker met de
apparaten doet staat in `fleet.json` in git, met per wijziging wie hem maakte
en waarom. De verantwoordelijke kan dus op elk moment zien wat de geldende
instructie is, en de historie ervan.

De verwerker meldt het onmiddellijk als een instructie naar zijn oordeel in
strijd is met de AVG.

## 3. Vertrouwelijkheid

Iedereen die toegang heeft is tot geheimhouding verplicht. Toegang wordt
verleend per rol en per bereik (organisatie, groep, apparaat) en wordt bij
elk verzoek opnieuw bepaald uit de directory - een ingetrokken
groepslidmaatschap werkt onmiddellijk door.

## 4. Beveiliging

De verwerker treft de maatregelen uit **bijlage B**. Die bijlage beschrijft
wat er is, niet wat er zou moeten zijn, inclusief de punten die op de
ingangsdatum nog openstaan.

## 5. Subverwerkers

Toestemming vooraf voor elke subverwerker. Op de meetdatum:

| Subverwerker | Waarvoor | Waar |
|---|---|---|
| Leafcloud | object storage voor databaseback-ups, bestemming vanaf de migratie (`leafcloud.store`) | te bevestigen |
| Hetzner | object storage voor databaseback-ups, houdt de back-ups van vóór de migratie | Duitsland (nbg1) |
| de hostingpartij van het cluster | draaien van de console | in te vullen |

Geen doorgifte buiten de EER.

**De opslag migreert van Hetzner naar Leafcloud.** Beide staan hier omdat ze er
beide zijn: nieuwe back-ups gaan naar Leafcloud, en Hetzner houdt de oudere
zolang die bewaartermijn loopt. Een subverwerker die nog data onder zich heeft
schrappen zou deze bijlage onjuist maken, niet actueler. De einddatum van die
periode is in te vullen; daarna vervalt de tweede regel.

De back-up van de observed plane draait. Gemeten op 21 augustus 2026 op de
productiecel: `ContinuousArchiving` en `LastBackupSucceeded` beide waar, de
laatste geslaagde back-up van 20 augustus 02:45 UTC, en een herstelpunt dat
teruggaat tot 17 augustus 22:51 UTC. De bewaartermijn is 30 dagen.

De verwerker meldt voorgenomen wijzigingen tijdig genoeg dat de
verantwoordelijke bezwaar kan maken.

## 6. Bijstand aan de verantwoordelijke

- **Rechten van betrokkenen.** De console heeft sinds 7 augustus 2026 een
  wisroute (art. 17) die de gegevens van één persoon verwijdert en **altijd
  rapporteert wat níet verwijderd kon worden** - de git-historie in het
  bijzonder. Inzage (art. 15) is mogelijk maar handmatig; daar is geen
  productfunctie voor.
- **Datalekken.** De verwerker meldt een inbreuk zonder onnodige
  vertraging, en in elk geval binnen 24 uur na ontdekking, zodat de
  verantwoordelijke zijn eigen 72-uurstermijn haalt.
- **DPIA.** Zie [dpia-draft.md](dpia-draft.md); de verwerker levert de
  technische feiten en houdt ze actueel.

## 7. Bewaartermijnen

Ingesteld en werkend sinds 7 augustus 2026:

| Gegeven | Termijn |
|---|---|
| Notificaties aan beheerders | 180 dagen |
| Verhogingsverzoeken | 365 dagen |
| Identiteiten van beheerders | 365 dagen |
| Check-ins van apparaten | 180 dagen, en alleen voor apparaten die de vloot niet meer kent |
| Diagnostiekbundels | 14 dagen |
| Auditspoor (git) | onbeperkt, zie hieronder |

**Dit zijn standaardwaarden van de leverancier en geen afspraak.** De
verantwoordelijke bevestigt of wijzigt ze; tot dat besluit gelden ze bij
gebrek aan beter.

**Het auditspoor is een uitzondering die vastgelegd moet worden.** Elke
configuratiewijziging is een git-commit; dat is vereist vanuit de BIO en is
niet met terugwerkende kracht te wissen. De verantwoordelijke legt vast dat
verantwoordingsgegevens een eigen grondslag en bewaartermijn hebben.

## 8. Einde van de overeenkomst

Bij beëindiging kiest de verantwoordelijke: teruggave of verwijdering.

**In dit geval is teruggave eenvoudig en dat is bewust zo ontworpen.** De
configuratie is een git-repository die de verantwoordelijke al heeft, en op
elk apparaat staat een kloon. De waargenomen toestand is één
PostgreSQL-database, waarvan een back-up en een uitgevoerd herstelpad
bestaan.

**Eén punt dat expliciet moet:** de database bevat de LUKS-herstelsleutels
van de apparaten, versleuteld. De sleutel die ze opent
(`SEXTANT_SECRET_KEY`) zit **niet** in de back-up. Bij beëindiging moeten
beide overgedragen worden, of de gemeente houdt gegevens over die ze niet kan
openen.

## 9. Audit

De verantwoordelijke mag jaarlijks controleren, en vaker bij een concrete
aanleiding. De verwerker levert de auditdocumenten in `docs/audit/` en werkt
mee aan onderzoek ter plaatse.

---

## Bijlage A - Verwerkingen

Zie [processing-register.md](processing-register.md). Tien verwerkingen, per
stuk met doel, betrokkenen en bewaartermijn, gemeten tegen de draaiende
omgeving.

## Bijlage B - Beveiligingsmaatregelen

Wat er is, gemeten op 7 augustus 2026:

- **Toegang** per rol en per bereik, elke aanvraag opnieuw bepaald uit de
  directory. Wachtwoorden nooit opgeslagen; tokens gehasht met argon2id en
  vergeleken in constante tijd, met een verplichte geldigheidsduur.
- **Apparaatidentiteit**: elk apparaat heeft een eigen sleutel die aan zijn
  eigen naam gebonden is. Een gedeelde sleutel bestond en is op 7 augustus
  verwijderd.
- **Versleuteling**: volledige schijfversleuteling op de apparaten met
  TPM2-ontgrendeling; herstelsleutels versleuteld bewaard.
- **Vier-ogen en review**: configuratiewijzigingen kunnen een
  wijzigingsverzoek en een tweede goedkeurder vereisen, en worden vóór
  invoering machinaal gevalideerd.
- **Gefaseerde uitrol** met soak-tijd en gezondheidsdrempels, zodat een
  slechte wijziging op een kleine groep strandt.
- **Logging**: elke wijziging is een commit met wie, wat, wanneer en waarom.
- **Back-up**: dagelijks buiten het cluster, met doorlopende WAL-archivering.
  Het herstelpad is op 7 augustus 2026 daadwerkelijk uitgevoerd en de
  gegevens kwamen byte voor byte terug.

**Openstaand op de meetdatum, en dit hoort in de overeenkomst en niet in een
voetnoot:**

- Aanmelden op apparaten gaat nog over onversleuteld LDAP. De console is om;
  de apparaten niet. Auditbevinding H3.
- De console pusht naar de forge met een machine-account sinds 7 augustus;
  bevestiging dat het spoor klopt volgt bij de eerstvolgende wijziging.
- Er is geen kwetsbaarhedenrapportage (CVE) over de vloot.
- Sleutelbeheer voor `SEXTANT_SECRET_KEY` is niet belegd.

De verantwoordelijke tekent deze overeenkomst in de wetenschap van deze
punten, of stelt voorwaarden aan het sluiten ervan. Ze weglaten zou de
overeenkomst juister laten lijken dan de werkelijkheid.
