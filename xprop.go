package main

import (
	"fmt"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

func setAlwaysOnTop(xid uint32) error {
	conn, err := xgb.NewConn()
	if err != nil {
		return fmt.Errorf("x11 connect: %w", err)
	}
	defer conn.Close()

	setup := xproto.Setup(conn)
	root := setup.DefaultScreen(conn).Root

	wmState, err := internAtom(conn, "_NET_WM_STATE")
	if err != nil {
		return err
	}

	wmStateAbove, err := internAtom(conn, "_NET_WM_STATE_ABOVE")
	if err != nil {
		return err
	}

	// _NET_WM_STATE client message: data32[0]=1 (add), data32[1]=atom
	ev := xproto.ClientMessageEvent{
		Format: 32,
		Window: xproto.Window(xid),
		Type:   wmState,
		Data: xproto.ClientMessageDataUnionData32New([]uint32{
			1, // _NET_WM_STATE_ADD
			uint32(wmStateAbove),
			0, 0, 0,
		}),
	}

	mask := uint32(xproto.EventMaskSubstructureRedirect | xproto.EventMaskSubstructureNotify)
	return xproto.SendEventChecked(conn, false, root, mask, string(ev.Bytes())).Check()
}

func internAtom(conn *xgb.Conn, name string) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, fmt.Errorf("intern atom %s: %w", name, err)
	}
	return reply.Atom, nil
}
