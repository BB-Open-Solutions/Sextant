# Acceptatiedraaiboek e2e-3 en e2e-4 (weg naar 1.0.0)

Twee runs over dezelfde console en hetzelfde device:

- **Run A — e2e-3, zonder integraties.** Bewijst dat Sextant op eigen benen
  staat: enrollen, inspoelen, convergeren, besturen, aantonen. Geen NetBird,
  geen LDAP, geen Wazuh, geen OpenBao.
- **Run B — e2e-4, met integraties.** Zelfde device, integraties erbij.

De volgorde is niet vrijblijvend. Run A eerst, want elke storing in Run B is
anders niet toe te wijzen: een device dat niet convergeert terwijl SSSD,
NetBird en Wazuh tegelijk aan staan levert vier verdachten en geen dader. Dat
is precies hoe e2e-2 tijd verloor.

Versie onder test: **console 0.69.0** (prod), overlay `bb-open` main, core
DAWO-NixOS zoals gepind in de ring.

## Hoe je dit invult

Elke regel heeft een **actie** en een **bewijs**. Het bewijs is wat je
opschrijft — niet "werkt", maar wat je zag. Een stap zonder waarneembaar
bewijs is niet getest, ook niet als er niets misging.

Noteer per regel: `OK`, `FOUT` (+ wat je zag), of `NVT` (+ waarom). Bevindingen
komen in `docs/e2e-3-findings.md` respectievelijk `docs/e2e-4-findings.md`, in
dezelfde vorm als `e2e-2-findings.md`: symptoom, oorzaak, bewijs, fix.

Twee valkuilen uit eerdere rondes, expliciet omdat ze allebei geld kostten:

1. **Meet één ding per keer.** In e2e-2 gingen er vijf fixes in vóór één
   meting; welke van de vijf het deed is nooit vastgesteld.
2. **Geloof de melding niet, meet de toestand.** `flux reconcile` meldt
   "applied" en `kubectl rollout status` meldt "rolled out" terwijl de oude
   pod blijft draaien. Vraag de image-tag op, niet de status.

## Voorbereiding (eenmalig, vóór Run A)

| # | Actie | Bewijs |
|---|---|---|
| P1 | Console-versie vaststellen | footer of orgpagina toont de versie na inloggen; van buiten geven `/status` en `/metrics` een 404 |
| P2 | Device volledig wissen | disk gewist, firmware in setup mode als Secure Boot in scope is |
| P3 | Ring-pin bewust achterlaten op main | de ring wijst naar een oudere revisie dan main — dit is de #16-conditie |
| P4 | Testgroep en testring leeg opzetten | groep zichtbaar in `/groups`, ring in `/updates/rollout` |
| P5 | Alle integratie-settings uit | `/integrations` toont alles uit op org en op de testgroep |

P3 is geen detail. Een device dat wordt ingespoeld terwijl zijn ring
achterloopt op main werd tot 0.69.0 geboren op de verkeerde revisie en kwam er
zelf niet meer uit. Dat is de enige taak die nog niet op hardware is bewezen,
dus de opstelling moet die conditie afdwingen in plaats van erop te hopen.

---

# Run A — zonder integraties

## A1. Console en toegang

| # | Actie | Bewijs |
|---|---|---|
| A1.1 | Inloggen via Zitadel | eigen naam op `/profile`, rol zichtbaar |
| A1.2 | Elke pagina openen | 200, volledig document, geen lege secties |
| A1.3 | Inloggen als lezer (niet-editor) | bewerkknoppen afwezig, niet alleen uitgegrijsd |
| A1.4 | Groep-scoped gebruiker | ziet alleen eigen groep in `/devices` en `/compliance` |
| A1.5 | Uitloggen | sessie weg, `/devices` stuurt naar login |
| A1.6 | `/status` en `/metrics` van buiten opvragen | **404** — allebei alleen op de interne poort; een publieke console hoort zijn versie niet te noemen |
| A1.7 | Build-identiteit in de footer en op de orgpagina | zichtbaar zodra je bent ingelogd |

A1.3 vraagt om echt kijken: een knop die er staat maar 403 geeft is een andere
bug dan een knop die er niet staat, en de tweede is de bedoeling.

## A2. Enrollment

| # | Actie | Bewijs |
|---|---|---|
| A2.1 | `/enroll` wizard doorlopen | device verschijnt in `/devices` met status "nooit gezien" |
| A2.2 | Hardwareprofiel kiezen | profiel op de devicepagina, disko-notities kloppen |
| A2.3 | Aan testgroep koppelen | `/groups` telt het device mee |
| A2.4 | Enrolltoken hergebruiken | tweede gebruik geweigerd |
| A2.5 | Device zonder groep | valt terug op org-scope, geen crash |

## A3. Inspoelen (station)

| # | Actie | Bewijs |
|---|---|---|
| A3.1 | Station opstarten, device aanmelden | station zichtbaar in `/station`, job claimbaar |
| A3.2 | Imagingjob starten | job krijgt status "claimed" |
| A3.3 | **Revisie in de job controleren** | de job draagt de **ring-pin**, niet main — dit is #16 |
| A3.4 | Installatie afronden | device boot, geen handmatige stap |
| A3.5 | Eerste check-in | device meldt zich met de ring-revisie |
| A3.6 | Host-key als age-recipient | secrets ontsleutelen op het device zonder handwerk |
| A3.7 | Rustige boot | geen kerneldebug over de console |

A3.3 is de kern van de avond. Kijk in de job zelf, niet in het eindresultaat:
als het device toevallig al op main stond is de fix niet bewezen.

## A4. Convergentie

| # | Actie | Bewijs |
|---|---|---|
| A4.1 | Setting wijzigen, mergen | device pakt hem binnen het interval |
| A4.2 | comin-status | draait op de juiste config; guard heeft niet hoeven ingrijpen |
| A4.3 | comin-config-guard forceren | stale config → guard herstart comin binnen een uur |
| A4.4 | Device uitzetten tijdens rollout | komt na aanzetten alsnog bij |
| A4.5 | Kapotte config aanbieden | device weigert en blijft op de oude generatie |

A4.5 hoort er expliciet in: een device dat een kapotte generatie *wel*
activeert is gevaarlijker dan een device dat achterloopt.

## A5. Settings

| # | Actie | Bewijs |
|---|---|---|
| A5.1 | Org-setting zetten | overerft naar groep en device |
| A5.2 | Groep overschrijft org | groepswaarde wint op het device |
| A5.3 | Device overschrijft groep | devicewaarde wint |
| A5.4 | Org-setting vergrendelen | groep kan hem niet meer verzwakken |
| A5.5 | Afhankelijke optie zonder zijn enable | grijs, met uitleg wanneer hij landt |
| A5.6 | Image-time optie wijzigen | zegt dat hij bij het inspoelen landt, niet nu |
| A5.7 | Lijstwaarde (bv. tijdservers) | regel per regel bewerkbaar, komt goed aan |
| A5.8 | Waarde terug op "overerven" | valt terug, blijft niet hangen |

## A6. Policies en condities

| # | Actie | Bewijs |
|---|---|---|
| A6.1 | Policy maken met settings | verschijnt in `/policies` |
| A6.2 | Toewijzen aan groep | devices in die groep krijgen de waarden |
| A6.3 | Sleutel in policy vergrendelen | lagere scope kan hem niet overschrijven |
| A6.4 | **Settings-editor openen op die groep** | de rij noemt de policy; vergrendeld staat als vergrendeld |
| A6.5 | Compliance-controls invullen (BIO/ISO) | tags op de policy-pagina, terug in de CSV-export |
| A6.6 | Conditie toevoegen (`disk.free_percent >= 15`) | policy accepteert hem |
| A6.7 | Kapotte conditie proberen | geweigerd bij opslaan, niet stil genegeerd |
| A6.8 | Device onder de drempel brengen | bevinding op `/compliance` mét de meting |
| A6.9 | Device dat niets meldt | **geen** bevinding — ongemeten is geen overtreding |
| A6.10 | Conditie weer halen | bevinding verdwijnt |

A6.9 is de regel die het gedrag draagt: een vloot die machines beschuldigt die
hij niet kan meten, leert operators de hele categorie te negeren.

## A7. Changes en gate

| # | Actie | Bewijs |
|---|---|---|
| A7.1 | Wijziging indienen | verschijnt in `/changes` met diff |
| A7.2 | Gate draait | bouwt, uitslag op de change |
| A7.3 | Gate laten falen | change is niet te mergen |
| A7.4 | Vierogen aanzetten | eigen change niet zelf goed te keuren |
| A7.5 | Tweede goedkeurder | merge lukt |
| A7.6 | Change intrekken | branch weg, geen wees in de lijst |
| A7.7 | Twee changes tegelijk | tweede rebaset of weigert netjes |

## A8. Rollout

| # | Actie | Bewijs |
|---|---|---|
| A8.1 | Ringen definiëren | `/updates/rollout` toont het plan |
| A8.2 | Wave promoten | alleen ring 1 krijgt de revisie |
| A8.3 | Soak afwachten | promoveert niet vóór de tijd om is |
| A8.4 | Gezondheidsdrempel niet gehaald | promotie stopt |
| A8.5 | Wave laten vastlopen | na het stall-venster een incident dat de devices noemt |
| A8.6 | `[risk:high]` in een change | extra bevestiging vereist |
| A8.7 | Auto-flow aan | promoveert vanzelf tot de laatste ring |
| A8.8 | Ring terugzetten (pin) | devices gaan terug, zonder handwerk |

## A9. Updatesbord en incidenten

| # | Actie | Bewijs |
|---|---|---|
| A9.1 | Device op de ring-revisie | staat als "komt overeen" — geen revisiehashes |
| A9.2 | Config-achterstand | **waarschuwing**, geen issue |
| A9.3 | Core-achterstand binnen de gratieperiode | waarschuwing |
| A9.4 | Core-achterstand voorbij 14 dagen | **issue** |
| A9.5 | Device offline > 2 weken | incident |
| A9.6 | Device dat nooit meldde | incident |
| A9.7 | Device met bouwfout | incident met de foutmelding |
| A9.8 | Onbekende revisie | incident, en niet verward met "nog niet geteld" |
| A9.9 | Vers ingespoeld device | **geen** melding van een out-of-band wijziging |

A9.2 tegen A9.4 is de splitsing die je gevraagd hebt: een config die
achterloopt is een waarschuwing, een systeem dat achterloopt wordt na verloop
van tijd een echt probleem.

## A10. Acties op afstand

| # | Actie | Bewijs |
|---|---|---|
| A10.1 | Diagnostiek opvragen | rapport terug op de devicepagina |
| A10.2 | Recovery key onthullen | onthulling in de audit, key klopt |
| A10.3 | Wipe-intent zetten op ongewapend device | device weigert, console meldt "geweigerd" |
| A10.4 | Wipe wapenen en uitvoeren | device bevestigt, disk-key vernietigd |
| A10.5 | Wipe die niet afrondt | incident "wipe niet voltooid" |

A10.3 en A10.5 zijn de belangrijkste van deze groep: een wipe die stil faalt
is het ergste dat dit product kan doen.

## A11. Secrets

| # | Actie | Bewijs |
|---|---|---|
| A11.1 | Secret toevoegen | versleuteld in git, klaartekst nergens |
| A11.2 | Nieuw device krijgt het | ontsleutelt zonder handmatige rekey |
| A11.3 | Rekey draaien | alle recipients bij, oude weg |
| A11.4 | Secret intrekken | device kan hem niet meer lezen |

## A12. Aantoonbaarheid

| # | Actie | Bewijs |
|---|---|---|
| A12.1 | Audit-log | elke wijziging met wie, wat, wanneer |
| A12.2 | Evidence-export | bestand met de assurance-configuratie |
| A12.3 | Devices-CSV | klopt met het scherm |
| A12.4 | Policies-CSV | controls staan erin |
| A12.5 | Service-account maken en gebruiken | werkt, staat in de audit |
| A12.6 | Notificatie afvuren | komt aan (of mail is bewust uit — dan NVT) |

## A13. USB-control en printen

| # | Actie | Bewijs |
|---|---|---|
| A13.1 | Allowlist vullen **via een policy** | regels landen in de config; de settings-editor biedt de sleutel niet meer aan |
| A13.2 | USB-control aanzetten in dezelfde policy | wat bij boot inzit blijft werken |
| A13.2b | Proberen de sleutel tóch via settings te zetten | geweigerd (403) — verbergen is geen handhaving |
| A13.3 | Toegestaan apparaat inpluggen | werkt |
| A13.4 | Niet-toegestaan apparaat inpluggen | geblokkeerd |
| A13.5 | Toetsenbord uit de allowlist laten | **vooraf bedacht**: dit sluit je buiten — alleen testen met een tweede weg naar binnen |
| A13.6 | Printen aanzetten | printer gevonden, testpagina eruit |

A13.5 met opzet als waarschuwing en niet als stap. De optie is `riskClass:
high` juist omdat een allowlist die het toetsenbord mist niet op afstand te
herstellen is.

## A14. Lokaal beheerdersaccount

| # | Actie | Bewijs |
|---|---|---|
| A14.1 | Aanzetten met naam + secret | inloggen lukt lokaal |
| A14.2 | Gereserveerde naam proberen | geweigerd bij het opslaan |
| A14.3 | Uitzetten | account op slot, inloggen lukt niet meer |

## A15. Gebruikersrechten

Log in als **`bbuijs` (directory-gebruiker)**, niet als de lokale beheerder.
Dat is het hele punt: dit werd gevonden doordat een LDAP-gebruiker in geen
enkele lokale groep zit, en met de lokale beheerder merk je er niets van.

| # | Actie | Bewijs |
|---|---|---|
| A15.1 | Wifi aan/uit zetten | gaat direct, geen dialoog |
| A15.2 | Netwerk kiezen uit de lijst | verbindt, geen dialoog |
| A15.3 | **Netwerk opslaan voor alle gebruikers** | vraagt om **je eigen** wachtwoord, niet om een beheerderswachtwoord |
| A15.4 | Meteen een tweede beveiligde actie | vraagt het niet opnieuw (het onthoudt kort) |
| A15.5 | Tijdzone wijzigen | gaat direct |
| A15.6 | **Klok handmatig zetten** | **geweigerd** — tijd komt van de vloot |
| A15.7 | **Hostnaam wijzigen** | **geweigerd** — die hoort bij de vloot |
| A15.8 | USB-stick koppelen | gaat direct |
| A15.9 | Dok aanmelden (raakt #18) | gaat direct, geen beheerder nodig |
| A15.10 | **Gebruikersbeheer openen (account toevoegen)** | **geweigerd** — nooit verleenbaar |
| A15.11 | Via SSH inloggen en `nmcli` een netwerk laten opslaan | **geweigerd** — geen zitplaats, dus geen recht |
| A15.12 | Printer toevoegen | **geweigerd** (printen staat uit op deze groep; recht is bewust niet verleend) |

A15.11 is de belangrijkste van de reeks. `session` betekent "wie fysiek aan de
machine zit", niet "wie een shell heeft". Slaagt dit wél, dan is de
`subject.local && subject.active`-clausule stuk en is elke andere regel hier
ook niets waard.

A15.6, A15.7 en A15.10 zijn negatieve tests. Een uitslag "gaat direct" is
daar een **fout**, geen succes — makkelijk verkeerd af te vinken als je door
de lijst raast.

Loopt er iets anders dan verwacht, kijk dan op het toestel mee terwijl je het
opnieuw probeert:

```
journalctl -f -u polkit
```

Dat noemt het actie-id dat geweigerd wordt, zodat we niet hoeven te gissen
welk recht ontbreekt.

## A16. Verzoek om verhoogde rechten

Log in als **`bbuijs`**. Zet een recht dat op `off` staat (bijvoorbeeld
`firmware`) even aan als testdoel, of gebruik een actie die nog om een
beheerder vraagt.

| # | Actie | Bewijs |
|---|---|---|
| A16.1 | Actie uitvoeren die om een beheerder vraagt | dialoog verschijnt |
| A16.2 | Kijk in de console op `/elevation` | verzoek staat er, met gebruiker, toestel en de wachttijd |
| A16.3 | Goedkeuren | dialoog op de laptop gaat door, **zonder** dat je een wachtwoord typte |
| A16.4 | Tweede verzoek, nu **weigeren** | dialoog valt terug op het wachtwoordveld |
| A16.5 | Derde verzoek, niets doen | na vijf minuten verlopen; verdwijnt uit de wachtrij |
| A16.6 | Verzoek doen met de console onbereikbaar | valt terug op het wachtwoordpad; dialoog blijft **niet** hangen |
| A16.7 | Gemelde actie op de kaart | staat er met het label "gemeld" — context, geen bewijs |

A16.6 is de belangrijkste. De hele constructie is `sufficient` en additief:
faalt hij, dan hoort het dialoog zich te gedragen zoals vóórdat deze functie
bestond. Blijft hij hangen, dan is dat erger dan een weigering — dan kun je
niet eens meer een wachtwoord intypen.

A16.4 hoort óók door te vallen naar het wachtwoordveld. Een weigering door de
operator sluit de gebruiker niet buiten; het zegt alleen dat er langs deze weg
geen goedkeuring komt.

---

# Run B — met integraties

Zet ze **één voor één** aan, met een check-in ertussen. Alles tegelijk
aanzetten is hoe e2e-2 vier storingen tegelijk kreeg en er drie van miste.

## B1. NetBird (eerst — de rest loopt erover)

| # | Actie | Bewijs |
|---|---|---|
| B1.1 | `netbird.enable` + setup-key op de groep | device verschijnt als peer in het dashboard |
| B1.2 | Route naar de clusterdiensten | device bereikt het interne adres |
| B1.3 | Herstart | peer komt vanzelf terug |
| B1.4 | Console blijft bereikbaar | check-ins lopen door |

## B2. Identity (LDAP/SSSD)

| # | Actie | Bewijs |
|---|---|---|
| B2.1 | `identity.enable` met LDAPS | `getent passwd <user>` vindt de gebruiker |
| B2.2 | Inloggen als LDAP-gebruiker | sessie op het device |
| B2.3 | Homedir | wordt aangemaakt |
| B2.4 | Verkeerd certificaat aanbieden | verbinding **geweigerd** — de strikte weg moet strikt zijn |
| B2.5 | LDAP tijdelijk onbereikbaar | offline login werkt binnen de geldigheidsduur |
| B2.6 | Instellingen wijzigen | nsncd ververst; geen oude gebruikersnaam blijft hangen |

B2.4 en B2.6 komen allebei uit e2e-2. SSSD zet `ldap_id_use_start_tls`
standaard aan en nscd/nsncd hield oude antwoorden vast — twee stille lagen die
er als een werkende opstelling uitzagen.

## B3. Wazuh

| # | Actie | Bewijs |
|---|---|---|
| B3.1 | `wazuh.enable` op de groep | agent registreert, manager toont hem **Active** |
| B3.2 | Agent-queue | overleeft een herstart |
| B3.3 | Gebeurtenis afvuren | komt aan bij de manager |
| B3.4 | Manager tijdelijk weg | agent hervat vanzelf |

B3.2 staat erin omdat precies dat in e2e-2 stukging: systemd zette de
rechten van de state-directory terug vóór élke start terwijl de binaries naar
de `wazuh`-gebruiker afschalen.

## B4. OpenBao

| # | Actie | Bewijs |
|---|---|---|
| B4.1 | Inschakelen | device haalt zijn materiaal op |
| B4.2 | Token intrekken | toegang stopt |

## B5. Mail

| # | Actie | Bewijs |
|---|---|---|
| B5.1 | SMTP invullen | testbericht komt aan |
| B5.2 | Notificatie op een incident | mail met bruikbare inhoud |

## B6. Alles tegelijk

| # | Actie | Bewijs |
|---|---|---|
| B6.1 | Vol device opnieuw inspoelen | komt met alle integraties op zonder handwerk |
| B6.2 | Volledige rollout over de ringen | geen integratie breekt bij een generatiewissel |
| B6.3 | Compliance-beeld | schoon, of alleen bevindingen die je verwacht |

B6.1 is de eigenlijke productvraag: "gemeente koopt bundel" betekent dat een
lege laptop met één inspoelactie op alles aangesloten is.

---

## Afronden

| # | Actie |
|---|---|
| C1 | Bevindingen wegschrijven per run |
| C2 | Elke FOUT wordt een taak, met symptoom en bewijs |
| C3 | Blokkerend voor 1.0.0 markeren |
| C4 | Draaiboek bijwerken waar een stap onduidelijk bleek |

C4 is geen formaliteit: elke stap waarbij je moest nadenken over wat "goed"
betekent, is een stap die de volgende keer verkeerd wordt uitgevoerd.
