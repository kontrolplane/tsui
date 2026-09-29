Set FontSize 20
Set Width 2000
Set Height 1000
Set BorderRadius 10
Set TypingSpeed 100ms

Output ./assets/cassette.gif

Hide
  Type@10ms "go build -o build/kontrolplane/tsui . && clear && ./build/kontrolplane/tsui"
  Enter
  Wait+Screen@60s /connected/
  Sleep 1s
Show

Sleep 6s

Screenshot ./assets/pages/stream/overview.png

Sleep 4s
Down@800ms 5
Sleep 3s
Enter

Sleep 6s

Screenshot ./assets/pages/stream/details.png

Sleep 4s
Down@800ms 3
Sleep 3s
Enter

Sleep 6s

Screenshot ./assets/pages/message/details.png

Sleep 4s
Type "q"

Sleep 4s
Tab
Sleep 5s

Screenshot ./assets/pages/stream/consumers.png

Sleep 4s
Down
Sleep 3s
Enter
Sleep 5s

Screenshot ./assets/pages/consumer/details.png

Sleep 4s
Type "q"
Sleep 4s
Tab

Sleep 4s
Ctrl+N
Sleep 4s
Type "nl.created"
Tab
Type "Nats-Msg-Id: ord_7291840"
Tab
Type '{"orderId": "ord_7291840", "customer": "cus_0042", "total": 18.5}'
Sleep 5s

Screenshot ./assets/pages/message/publish.png

Sleep 4s
Ctrl+S
Sleep 5s

Type "/"
Sleep 3s
Type "orders.eu.*"
Sleep 4s
Enter
Sleep 5s
Type "q"

Sleep 4s
Ctrl+P
Sleep 4s
Tab
Type "orders.us.>"
Enter
Sleep 4s

Screenshot ./assets/pages/stream/purge.png

Sleep 4s
Enter

Sleep 4s
Type "q"
Sleep 4s
Ctrl+N
Sleep 4s
Type "PAYMENTS"
Enter
Sleep 4s
Type "payments.>"
Sleep 4s

Screenshot ./assets/pages/stream/creation.png

Sleep 4s
Escape
Sleep 5s
