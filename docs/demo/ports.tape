Output "/tmp/ports.gif"
Set Width 1340
Set Height 560
Source docs/demo/common.tape

Type "dockup ports --managed --group"
Enter
Wait
Type "dockup port-check 80,3000,8443,9000 --proto tcp"
Enter
Wait
Sleep 500ms
Screenshot docs/img/ports.png
