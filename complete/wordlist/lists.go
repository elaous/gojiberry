package wordlist

// Word lists, one word per space-separated token.

const de = `aber alle allein alles also alt am Anfang andere ändern anders Antwort Arbeit arbeiten auch auf aus Auto
bald bauen beginnen bei beide beim besser bestimmt Bild bin bis bitte bleiben brauchen bringen Buch dabei dafür dann
darf darüber das dass dein denken denn der deshalb dieser doch dort drei dürfen durch eigentlich einfach einmal Ende
endlich Entscheidung er Erde erst erste Erfahrung erklären erzählen es essen etwas euch fahren Familie fast Fehler
Fenster fertig finden Frage fragen Frau Freund früh fünf ganz geben gehen gemeinsam genau gerade gern Geschichte
gestern gesund gewesen glauben gleich groß gut haben halten Hand Haus heute hier hoffen hören Idee immer Information
Jahr jede jemand jetzt Kind klar klein kommen können Kopf kurz lang lassen laufen leben legen leicht lernen lesen
letzte Leute Licht lieben machen Mann manchmal Meinung mehr mein Menschen Minute mit morgen müssen nach nächste
Nacht nahe nehmen neu nicht nichts nie niemand noch nur oben oder oft ohne Ordnung Platz Problem Rechnung reden
richtig sagen schnell schon schreiben schwer sehen sehr sein selbst setzen sicher sie sind sitzen so sollen sondern
Sprache sprechen Stadt stehen Stelle Stunde suchen Tag Text Thema Tisch Tür über Uhr um und unser unter Urlaub
Vater verstehen viel vielleicht vier von vor wahrscheinlich Wasser Weg weil weiß weit welche Welt wenig wenn werden
wichtig wieder wir wirklich wissen wo Woche wohnen wollen Wort wünschen zehn Zeit zeigen Zimmer zurück zusammen zwei
zwischen Absatz Dokument Datei Absender Adresse Ausgabe Bearbeiten Einstellung Ergebnis Format Gruppe Kommentar
Kapitel Liste Nachricht Programm Schrift Seite Speichern Tabelle Überschrift Vorschlag Wörterbuch Antrag Arbeitsplatz Aufgabe Ausnahme Beispiel Beruf Beschreibung besonders Betrieb Bewegung Bildung Dank Daten Ebene Einladung Entwicklung Erlaubnis Ersatz Fall Firma Folge Freiheit Frühling Gebiet Gefühl Gesellschaft Gesetz Gespräch Grund Hilfe Kosten Kunde Lösung Material Mitarbeiter Möglichkeit Nutzer Ort Person Preis Projekt Qualität Regel Ressource Sicherheit Sommer Termin Unterschied Verbindung Vergleich Verfügung Versuch Vertrag Verwaltung Voraussetzung Wahl Wirtschaft Zahl Ziel Zugang Zukunft Zusammenhang`

const fr = `à abord accord acheter afin ailleurs aimer ainsi aller alors ami an année appeler après arriver assez attendre
aucun aussi autre avant avec avoir beaucoup besoin bien bientôt blanc bon bonjour bureau cause cela celui cent
chaque chambre chez choisir chose combien commencer comme comment comprendre compter connaître continuer dans
dedans demain demander depuis dernier derrière devant devenir devoir dire donc donner dont dormir droit eau école
écrire encore enfant ensemble entendre entre entrer envoyer espérer essayer était être eux exemple expliquer faire
falloir famille femme fenêtre fille fils finir fois fort france frère grand gros guerre heure homme hôtel idée il
important jamais jeune jour juste laisser langue lequel lettre lire livre loin longtemps lumière main maintenant
mais maison malgré manger marché mauvais même mère merci mettre mieux moins moment mon monde montrer mot mourir
nécessaire ni nom nombre nous nouveau nuit ouvrir pain par parce parler partir pas passer payer pendant penser père
personne petit peu peut-être pièce place plus plusieurs porte porter pouvoir premier prendre près présent problème
programme question quand quelque quelqu'un rendre rentrer répondre rester retour réussir rien rouge rue sans savoir
selon semaine sentir seulement si sortir souvent suivre sujet tant temps tenir terre tête tous tout toujours tourner
travail travailler très trois trop trouver tu vendre venir ville vite voir voiture voix vouloir vrai vraiment
adresse chapitre colonne commentaire document dictionnaire fichier format groupe liste message page paragraphe
phrase police suggestion tableau titre texte enregistrer affaire ancien animal appareil argent article avenir bureau cadre capacité carte centre changement chemin choix classe clef client compte condition confiance connexion corps côté courant décision demande développement direction disponible document droit effet équipe espace état étude événement exercice expérience façon fonction fond groupe histoire image impossible information intérêt jardin journée lieu limite marque matin médecin membre méthode milieu mission modèle musique niveau objet occasion ordre papier partie période plan point possible pouvoir prix processus produit projet qualité raison région règle réponse résultat sécurité service situation société soir solution sujet système table total utilisateur valeur version`

const es = `a abajo abrir acabar acuerdo además agua ahí ahora algo alguien algún allí alto amigo ante antes año aparecer
aquel aquí así aunque ayer bajo bastante bien blanco buen buscar cabeza cada calle cambiar camino casa casi causa
cerca cierto cinco ciudad claro comer cómo como comprar comprender con conocer contar contra cosa creer cuál
cuando cuánto cuatro cuenta dar de deber decir dejar del demás dentro desde después día diez dinero donde dormir
dos durante edad el ella ellos empezar en encontrar entonces entrar entre escribir escuchar esperar esta estar este
estudiar existir explicar familia favor fin forma fuerte gente grande gustar haber hablar hacer hasta hay hijo
hombre hora hoy idea importante jamás joven junto lado largo lejos leer lengua libro luego lugar llamar llegar
llevar madre mal mañana mano más mayor mejor menos mes mesa mi mientras mirar momento mucho mujer mundo muy nada
necesitar negro ni niño no noche nombre nuevo nunca ocho otro padre pagar palabra papel para parecer parte pasar
pedir pensar peor pequeño perder pero persona poder poco poner por porque primero problema pronto pueblo puerta
que quedar querer quien quizá razón recibir recordar respuesta rojo saber sacar salir según seguir seis semana
sentir señor ser si siempre siete sin sobre solo tan tanto tarde tener tiempo todavía todo trabajar tres tu último
un uno usar valer varios vender venir ver vez vida viejo vivir volver voz ya
carta columna comentario diccionario documento dirección ejemplo encabezado fichero formato grupo lista mensaje
página párrafo programa sugerencia tabla texto título frase guardar acción actividad algunos amor análisis aplicación área base camino campo capacidad caso centro cambio color comunidad conjunto control cuerpo cuestión datos decisión derecho desarrollo dirección ejemplo empresa equipo error espacio estado estudio evento experiencia fecha función futuro grupo historia imagen importancia información interés jardín lugar manera material medio método mismo modelo nivel norte número objeto opción orden país parte periodo plan posible precio proceso producto proyecto punto región regla relación resultado seguridad servicio sistema situación sociedad solución tema tipo usuario valor versión`

const pt = `abrir acabar aceitar acima acordo adeus agora agradecer água ainda ajudar algo alguém algum alto amanhã amigo
amor andar ano antes antigo aparecer aprender aqui assim atenção atrás através bairro baixo banco bastante bem
beber bom branco brincar buscar cabeça cada cair caminho campo cansado carro casa causa cedo centro certo
chamar chegar cheio cidade cinco claro coisa começar comer como comprar compreender conhecer conseguir conta
continuar corpo correr cortar costume criança cuidado dar decidir deixar dentro depois desculpa desde destino
dever dia diferente difícil dinheiro direito dizer dois dormir dúvida durante embora empresa encontrar ensinar
entender entrar entre escola escolher escrever escutar espaço esperar estado estar estudar exemplo explicar
fácil falar família fazer feliz fechar fim final forma forte frente futuro ganhar gente gostar grande grupo
guerra história hoje homem hora idade ideia igual importante informação interesse ir jamais janela jogo jovem
junto lado lei lembrar lento ler levar liberdade lição ligar lindo língua livro lugar lutar luz mãe mais maior
manhã mão mar mesa mesmo meio melhor menos mês metade mexer minuto modo momento morar morrer mostrar mudar
muito mulher mundo música nada namorar nascer necessário negócio noite nome normal nota nova número obra olhar
ouvir outro pagar país palavra papel para parar parecer parte passar pedir pensar perder perguntar perto
pessoa pode poder pobre ponto porta porque pouco praça precisar preço primeiro problema procurar produto
programa pronto próximo quando quanto quarto quatro quem quente querer rápido razão receber recordar rede
resposta resultado rico rua saber sair sala saúde segundo seguir semana sempre sentir ser serviço sete
sociedade sol sozinho tarde tempo terra trabalhar trabalho trazer três último um usar valor vários velho
vender venda ver verdade vez viagem vida vir visita viver voltar vontade voz documento arquivo frase parágrafo
título tabela coluna linha página capítulo comentário sugestão dicionário idioma mensagem endereço formato
gravar salvar imprimir texto lista resumo`

const ar = `أبدا أبيض أخ أخبار أخذ أخرى أخضر أخيرا أرض أريد أسبوع أسرة أصبح أعرف أعمل أفضل أكثر ألف أم أمام أمس أمل أن أنا
أنت إلى إذا اثنان احد اسم استطاع استمر اسود اشتري اعطى افكر البيت الحياة الدرس الذي الشمس العالم العمل القلب
الليل المدرسة المدينة الوقت اليوم امرأة امر انتظر انتهى اين بحث بداية بعد بعض بيت بين تاريخ تحت تعلم تكلم تمام
توقف ثلاثة ثم جاء جدا جديد جميل جميع جنب جواب حاول حتى حديث حرب حركة حرف حسن حقيقة حلم حول حياة خارج خبر خير
داخل دائما درس دقيقة دولة دين ذلك ذهب رأس رجل رسالة رقم زمن زوج سؤال ساعة سبب سريع سلام سنة سوف سهل شارع شباب
شخص شرق شعب شمس شيء صباح صحيح صديق صغير صورة ضد طفل طريق طويل ظهر عام عائلة عالم عبر عدد عربي عرف عشرة عقل علم
على عمر عند عن عين غرفة غير فتح فجأة فكرة فوق قال قبل قدم قرأ قريب قصة قليل قوي كاف كان كبير كتاب كثير كلمة كل
كم كيف لا لأن لذلك لعب لغة لقاء لكن لماذا لم ليس ما ماذا مثل مجرد مدرسة مرة مساء مع معنى مفتاح مكان ملك من منذ
موضوع ميزان نحن نص نظام نعم نفس نهاية نور هذا هذه هل هناك هنا هو هي واحد وجه وحيد ودي وقت وقف ولد يد يمكن يوم
وثيقة ملف فقرة عنوان جدول عمود سطر صفحة فصل تعليق اقتراح قاموس برنامج تنسيق حفظ طباعة قائمة مجموعة ملخص`

const he = `אבא אבל אדם אוהב אוכל אולי אור אות אותו אחד אחרי אחרון אחר איזה איך אין איפה אישה אכן אל אלה אמא אמר אנחנו אני
אנשים אסור אף אפשר אפילו ארבע ארוך ארץ אתה אתמול בא בבית בגלל בדיוק בהחלט בזמן בחוץ בטוח בית בין בכל בלי במקום
בסדר בעיה בעצם בקשה בשביל בקרוב בוקר בן בעל ברור גדול גם גר דבר דרך דקה הבית הוא היא היה היום הכל הלילה הם
המון הנה הרבה הרי השנה הזמן זה זאת זמן זכר חדש חזרה חיים חייב חכם חלק חם חשוב חשבון טוב טעם יד ידוע יום יותר
יחד יכול ילד ילדים יפה ים ישן ישראל כאן כבר כדי כולם כותב כל כמה כמו כן כסף כתב לא לאט לאן לב לבד לבן לדעת
להיות לחם לחשוב ללכת למה למדוד לעבוד לפני לפעמים לקחת לקרוא לראות מאד מאוד מבין מה מהר מוכן מחר מחשב מי מיד
מילה מים מכאן מלא מעט מעל מקום מקבל מרוצה משפחה משהו מתי נגד נדמה נוסף נושא נכון נעים נראה נשים סביב ספר סוף
סיפור עבודה עד עוד עולם עושה עין על עם עצמו עשר עכשיו עיר ערב פה פעם פתאום פתח צריך צעיר קודם קטן קל קרוב קשה
ראש ראשון רגע רוצה רחוק רק שאלה שבוע שבת שוב שולחן שום שחור שיר של שלום שלוש שם שמח שנה שני שעה שקט תמיד תמונה
תוך תודה תורה תחת תן תשובה מסמך קובץ פסקה כותרת טבלה עמודה שורה עמוד פרק הערה הצעה מילון שפה הודעה תוכנית
כתובת עיצוב שמירה פתיחה הדפסה טקסט רשימה קבוצה סיכום`
