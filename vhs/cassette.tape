Set FontSize 20
Set Width 2000
Set Height 1000
Set BorderRadius 10

Output ./assets/cassette.gif

Hide
  Type "go build -o build/kontrolplane/tsui . && clear"
  Enter
  Copy "./build/kontrolplane/tsui"
  Paste
  Enter
Show

Sleep 4s

Screenshot ./assets/pages/stream/overview.png

Sleep 2s
Down@500ms 5
Sleep 1s
Enter

Sleep 4s

Screenshot ./assets/pages/stream/details.png

Sleep 2s
Down@500ms 3
Sleep 1s
Enter

Sleep 4s

Screenshot ./assets/pages/message/details.png

Sleep 2s
Type "q"

Sleep 2s
Tab
Sleep 3s

Screenshot ./assets/pages/stream/consumers.png

Sleep 2s
Down
Sleep 1s
Enter
Sleep 3s

Screenshot ./assets/pages/consumer/details.png

Sleep 2s
Type "q"
Sleep 2s
Tab

Sleep 2s
Ctrl+N
Sleep 2s
Type "nl.created"
Tab
Type "Nats-Msg-Id: ord_7291840"
Tab
Type '{"orderId": "ord_7291840", "customer": "cus_0042", "total": 18.5}'
Sleep 3s

Screenshot ./assets/pages/message/publish.png

Sleep 2s
Ctrl+S
Sleep 3s

Type "/"
Sleep 1s
Type "orders.eu.*"
Sleep 2s
Enter
Sleep 3s
Type "q"

Sleep 2s
Ctrl+P
Sleep 2s
Tab
Type "orders.us.>"
Enter
Sleep 2s

Screenshot ./assets/pages/stream/purge.png

Sleep 2s
Enter

Sleep 2s
Type "q"
Sleep 2s
Ctrl+N
Sleep 2s
Type "PAYMENTS"
Enter
Sleep 2s
Type "payments.>"
Sleep 2s

Screenshot ./assets/pages/stream/creation.png

Sleep 2s
Escape
Sleep 3s
