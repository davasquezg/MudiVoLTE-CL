#!/bin/sh
# The phone line on the router's touchscreen: an app for MudiScreen, an open
# user interface for the Mudi 7 display. MudiScreen runs it as
#   app.sh status         print what to show (JSON: a card, lock screen lines
#                         and a page with the phones and the missed calls)
#   app.sh action clear   clear the missed calls
# It reads the files of mudi-imsd. Texts are English; MudiScreen translates
# them with its catalogs. The lock screen shows no numbers, only counts.
# A call on the line shows on the card, the lock screen and in the status
# bar, with its time ({elapsed:T}: MudiScreen keeps it running); its
# number only behind the passcode.

. /usr/share/libubox/jshn.sh

STATE=/var/run/mudi-volte/state.json
MISSED=/etc/mudi-volte/missed.jsonl

phase_text() {
	case "$1" in
	starting) echo "starting" ;;
	modem) echo "waiting for the modem and LTE" ;;
	pdn) echo "opening the IMS connection" ;;
	registering) echo "registering with the operator" ;;
	recovering) echo "recovering after a failure" ;;
	radio-restart) echo "restarting the radio" ;;
	radio-off) echo "the radio is off (airplane mode)" ;;
	*) echo "$1" ;;
	esac
}

ago() { # SECONDS
	s=$1
	if [ "$s" -lt 60 ]; then
		echo "under a minute"
	elif [ "$s" -ge 86400 ]; then
		echo "$((s / 86400)) d $((s % 86400 / 3600)) h"
	elif [ "$s" -ge 3600 ]; then
		echo "$((s / 3600)) h $((s % 3600 / 60)) min"
	else
		echo "$((s / 60)) min"
	fi
}

section() { # LABEL
	json_add_object ""
	json_add_string type section
	json_add_string label "$1"
	json_close_object
}

info() { # LABEL VALUE [COLOR]
	json_add_object ""
	json_add_string type info
	json_add_string label "$1"
	json_add_string value "$2"
	[ -n "${3:-}" ] && json_add_string color "$3"
	json_close_object
}

status() {
	phase='' registered=0 since='' rat='' iface='' pid='' phones=''
	cdir='' cphase='' cstart='' cphone='' cpeer=''
	if [ -r "$STATE" ]; then
		eval "$(jsonfilter -i "$STATE" -e 'phase=@.phase' -e 'registered=@.registered' -e 'since=@.since' \
			-e 'rat=@.rat' -e 'iface=@.interface' -e 'pid=@.pid' 2>/dev/null)"
		phones=$(jsonfilter -i "$STATE" -e '@.phones[*]' 2>/dev/null | sed ':a;N;$!ba;s/\n/, /g')
		eval "$(jsonfilter -i "$STATE" -e 'cdir=@.call.dir' -e 'cphase=@.call.phase' -e 'cstart=@.call.start' \
			-e 'cphone=@.call.phone' -e 'cpeer=@.call.peer' 2>/dev/null)"
	fi
	alive=0
	[ -n "$pid" ] && kill -0 "$pid" 2>/dev/null && [ "$phase" != stopped ] && alive=1
	[ "$alive" = 1 ] || registered=0 cdir=
	missed=0
	[ -r "$MISSED" ] && missed=$(grep -c . "$MISSED")
	dur=''
	if [ -n "$since" ]; then
		start=$(date -d "$(echo "$since" | sed -E 's/^([0-9-]+)T([0-9:]+).*/\1 \2/')" +%s 2>/dev/null)
		[ -n "$start" ] && dur=$(ago $(($(date +%s) - start)))
	fi

	if [ "$registered" = 1 ]; then
		title="Calls are on" color=green state=online sub="VoLTE · $rat" subcol=dim
		if [ "$missed" -gt 0 ]; then
			sub="missed: $missed" subcol=orange
		fi
	elif [ "$alive" = 1 ]; then
		title="Calls: connecting" color=yellow state=$(phase_text "$phase")
		sub=$state subcol=yellow
	else
		title="Calls are down" color=red state="service stopped" sub=$state subcol=red
	fi
	# A call on the line comes first. The lock screen line has no number.
	ctitle=''
	if [ -n "$cdir" ]; then
		el="{elapsed:$cstart}" peer=${cpeer:-hidden number}
		# The card has room for a number and no more: which phone took
		# the call is on the page.
		csub=$peer
		case "$cdir:$cphase" in
		in:ringing) ctitle="Incoming call" ;;
		out:ringing) ctitle="Calling…" ;;
		in:* | out:*) ctitle="In a call · $el" ;;
		*:ringing) ctitle="Calling…" csub="$cphone → $cpeer" ;;
		*) ctitle="In a call · $el" csub="$cphone ↔ $cpeer" ;;
		esac
	fi

	json_init
	json_add_object card
	if [ -n "$ctitle" ]; then
		json_add_string title "$ctitle"
		json_add_string subtitle "$csub"
		json_add_string color green
	else
		json_add_string title "$title"
		json_add_string subtitle "$sub"
		json_add_string subtitle_color "$subcol"
		json_add_string color "$color"
	fi
	json_add_string icon phone
	json_add_string open main
	json_close_object

	if [ -n "$ctitle" ]; then
		json_add_array status
		json_add_object ""
		json_add_string icon phone
		json_add_string color green
		json_close_object
		json_close_array
	fi

	json_add_array lock
	json_add_object ""
	if [ -n "$ctitle" ]; then
		json_add_string text "$ctitle"
		json_add_string color green
	else
		json_add_string text "$title"
		json_add_string color "$color"
	fi
	json_close_object
	if [ "$missed" -gt 0 ]; then
		json_add_object ""
		json_add_string text "Missed calls: $missed"
		json_add_string color orange
		json_close_object
	fi
	json_close_array

	json_add_object pages
	json_add_object main
	json_add_string title "Calls (VoLTE)"
	json_add_array items
	if [ -n "$ctitle" ]; then
		section "Call"
		case "$cdir" in
		in)
			info "Incoming from" "$peer"
			[ -n "$cphone" ] && info "Answered on" "$cphone"
			;;
		out)
			info "Outgoing to" "$peer"
			info "From the phone" "$cphone"
			;;
		*) info "Between the phones" "$cphone → $cpeer" ;;
		esac
		if [ "$cphase" = ringing ]; then
			info "Ringing" "$el" green
		else
			info "Talking" "$el" green
		fi
		section "Line"
	fi
	info "Status" "$state" "$color"
	[ "$registered" = 1 ] && [ -n "$dur" ] && info "Online for" "$dur"
	[ -n "$iface" ] && info "Network" "$rat ($iface)"
	info "Phones" "${phones:-none}"
	json_add_object ""
	json_add_string type section
	json_add_string label "Missed calls ($missed)"
	json_close_object
	if [ "$missed" -gt 0 ]; then
		# The newest ten, newest first; time as DD.MM HH:MM.
		tail -n 10 "$MISSED" | sed -n '1!G;h;$p' >/tmp/mudi-screen-volte.$$
		while read -r line; do
			mt='' mf=''
			eval "$(jsonfilter -s "$line" -e 'mt=@.time' -e 'mf=@.from' 2>/dev/null)"
			json_add_object ""
			json_add_string type entry
			json_add_string detail "$(echo "$mt" | sed -E 's/^[0-9]{4}-([0-9]{2})-([0-9]{2})T([0-9]{2}:[0-9]{2}).*/\2.\1 \3/')"
			json_add_string label "${mf:-hidden number}"
			json_close_object
		done </tmp/mudi-screen-volte.$$
		rm -f /tmp/mudi-screen-volte.$$
		json_add_object ""
		json_add_string type button
		json_add_string label "Clear the list"
		json_add_string style danger
		json_add_object action
		json_add_string id clear
		json_add_string confirm "Clear the missed calls?"
		json_add_string confirm_label "Clear"
		json_add_boolean danger 1
		json_close_object
		json_close_object
	else
		json_add_object ""
		json_add_string type text
		json_add_string label "No missed calls"
		json_add_string color dim
		json_close_object
	fi
	json_close_array
	json_close_object
	json_close_object
	json_dump
}

case "${1:-}" in
status) status ;;
action)
	case "${2:-}" in
	clear)
		/usr/sbin/mudi-volte missed clear >/dev/null || exit 1
		echo "Cleared"
		;;
	*)
		echo "unknown action: ${2:-}" >&2
		exit 2
		;;
	esac
	;;
*)
	echo "usage: $0 status | action clear" >&2
	exit 2
	;;
esac
