package server

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	dhcp "github.com/krolaw/dhcp4"
	log "github.com/sirupsen/logrus"
)

// lease defines a lease that is allocated to a client
type lease struct {
	nic    string    // Client's Addr
	expiry time.Time // When the lease expires
}

// DHCPSettings -
type DHCPSettings struct {
	IP      net.IP       // Server IP to use
	Options dhcp.Options // Options to send to DHCP Clients
	Start   net.IP       // Start of IP range to distribute

	LeaseRange    int           // Number of IPs to distribute (starting from start)
	LeaseDuration time.Duration // Lease period
	Leases        map[int]lease // Map to keep track of leases
}

//ServeDHCP -
func (h *DHCPSettings) ServeDHCP(p dhcp.Packet, msgType dhcp.MessageType, options dhcp.Options) (d dhcp.Packet) {
	mac := p.CHAddr().String()
	log.Debugf("DCHP Message Type: [%v] from MAC Address [%s]", msgType, mac)
	switch msgType {
	case dhcp.Discover:
		free, nic := -1, mac
		for i, v := range h.Leases { // Find previous lease
			if v.nic == nic {
				free = i
				goto reply
			}
		}
		if free = h.freeLease(); free == -1 {
			return
		}
	reply:
		h.Options[dhcp.OptionVendorClassIdentifier] = h.IP
		// Reply should have the configuration details in for iPXE to boot from
		if string(options[dhcp.OptionUserClass]) == "iPXE" {
			deploymentType := FindDeployment(mac)
			// If this mac address has no deployment attached then reboot IPXE
			if deploymentType == "" {
				log.Warnf("Mac address[%s] is unknown, not returning an address", mac)
				return nil
			}
			// Assign the deployment boot script
			log.Infof("Mac address [%s] is assigned a [%s] deployment type", mac, deploymentType)
			// Convert the : in the mac address to dashes to make life easier
			dashMac := strings.Replace(mac, ":", "-", -1)
			// if an entry doesnt exist then drop it to a default type, if not then it has its own specific
			if httpPaths[fmt.Sprintf("%s.ipxe", dashMac)] == "" {
				h.Options[dhcp.OptionBootFileName] = []byte("http://" + h.IP.String() + "/" + deploymentType + ".ipxe")
			} else {
				h.Options[dhcp.OptionBootFileName] = []byte("http://" + h.IP.String() + "/" + dashMac + ".ipxe")
			}

		}
		ipLease := dhcp.IPAdd(h.Start, free)
		log.Debugf("Allocated IP [%s] for [%s]", ipLease.String(), mac)

		return dhcp.ReplyPacket(p, dhcp.Offer, h.IP, ipLease, h.LeaseDuration,
			h.Options.SelectOrderOrAll(options[dhcp.OptionParameterRequestList]))

	case dhcp.Request:

		if server, ok := options[dhcp.OptionServerIdentifier]; ok && !net.IP(server).Equal(h.IP) {
			return nil // Message not for this dhcp server
		}
		reqIP := net.IP(options[dhcp.OptionRequestedIPAddress])
		if reqIP == nil {
			reqIP = net.IP(p.CIAddr())
		}

		if len(reqIP) == 4 && !reqIP.Equal(net.IPv4zero) {
			if leaseNum := dhcp.IPRange(h.Start, reqIP) - 1; leaseNum >= 0 && leaseNum < h.LeaseRange {
				if l, exists := h.Leases[leaseNum]; !exists || l.nic == p.CHAddr().String() {
					h.Leases[leaseNum] = lease{nic: p.CHAddr().String(), expiry: time.Now().Add(h.LeaseDuration)}
					return dhcp.ReplyPacket(p, dhcp.ACK, h.IP, reqIP, h.LeaseDuration,
						h.Options.SelectOrderOrAll(options[dhcp.OptionParameterRequestList]))
				}
			}
		}
		return dhcp.ReplyPacket(p, dhcp.NAK, h.IP, nil, 0, nil)

	case dhcp.Release, dhcp.Decline:
		nic := p.CHAddr().String()
		for i, v := range h.Leases {
			if v.nic == nic {
				delete(h.Leases, i)
				break
			}
		}
	}
	return nil
}

func (h *DHCPSettings) freeLease() int {
	now := time.Now()
	b := rand.Intn(h.LeaseRange) // Try random first
	for _, v := range [][]int{{b, h.LeaseRange}, {0, b}} {
		for i := v[0]; i < v[1]; i++ {
			if l, ok := h.Leases[i]; !ok || l.expiry.Before(now) {
				return i
			}
		}
	}
	return -1
}