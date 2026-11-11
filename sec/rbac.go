// SPDX-License-Identifier: MIT

package sec

import "github.com/otfabric/go-iec60870-5/asdu"

// Permission is one of the permissions of IEC TS 60870-5-7 role-based access
// control.
type Permission uint8

// The permissions.
const (
	PermMonitorData Permission = 1 << iota
	PermOperateControls
	PermFileRead
	PermFileWrite
	PermFileManagement
	PermConfigOperation
	PermConfigSecurity
)

// AccessRole is a role of the controlling station at the controlled station.
type AccessRole struct {
	ID    int
	Name  string
	Perms Permission
}

// The pre-defined roles of IEC TS 60870-5-7.
var (
	RoleViewer    = AccessRole{0, "VIEWER", PermMonitorData | PermFileRead}
	RoleOperator  = AccessRole{1, "OPERATOR", PermMonitorData | PermOperateControls | PermFileRead}
	RoleEngineer  = AccessRole{2, "ENGINEER", PermMonitorData | PermFileRead | PermFileWrite | PermFileManagement | PermConfigOperation}
	RoleInstaller = AccessRole{3, "INSTALLER", PermMonitorData | PermFileRead | PermFileWrite | PermConfigOperation}
	RoleSecAdmin  = AccessRole{4, "SECADM", PermFileRead | PermFileWrite | PermFileManagement | PermConfigSecurity}
	RoleSecAudit  = AccessRole{5, "SECAUD", PermFileRead}
	RoleRBACMaint = AccessRole{6, "RBACMNT", PermFileRead | PermFileWrite | PermFileManagement | PermConfigSecurity}
)

// Roles are the pre-defined roles, by ID.
var Roles = []AccessRole{RoleViewer, RoleOperator, RoleEngineer, RoleInstaller, RoleSecAdmin, RoleSecAudit, RoleRBACMaint}

// Has reports whether the role grants p. File write includes file read.
func (r AccessRole) Has(p Permission) bool {
	perms := r.Perms
	if perms&PermFileWrite != 0 {
		perms |= PermFileRead
	}
	return perms&p == p
}

// RequiredPermission is the permission a request of type t needs. The
// standard leaves the mapping to the implementation; this one follows
// github.com/cB-Sigma/iec60870: commands and set points need operate
// controls; interrogation and read need monitor data; parameters and the
// reset process need config operation; the file service needs file read
// (call, directory, log query, and the acknowledgement of what was read) or
// file write. Clock synchronization, test and everything else need nothing.
//
// F_AF_NA_1 needs file read: from a controlling station it can only
// acknowledge a section or file it downloaded, since in an upload the
// controlled station acknowledges. cB-Sigma/iec60870 v0.3.0 asks for file
// write, which lets a viewer read a whole file and then refuses its
// acknowledgement.
func RequiredPermission(t asdu.TypeID) Permission {
	switch {
	case t >= asdu.C_SC_NA_1 && t <= asdu.C_BO_NA_1, t >= asdu.C_SC_TA_1 && t <= asdu.C_BO_TA_1:
		return PermOperateControls
	case t == asdu.C_IC_NA_1 || t == asdu.C_CI_NA_1 || t == asdu.C_RD_NA_1:
		return PermMonitorData
	case t == asdu.C_RP_NA_1, t >= asdu.P_ME_NA_1 && t <= asdu.P_AC_NA_1:
		return PermConfigOperation
	case t == asdu.F_SC_NA_1 || t == asdu.F_SC_NB_1 || t == asdu.F_DR_TA_1 || t == asdu.F_AF_NA_1:
		return PermFileRead
	case t >= asdu.F_FR_NA_1 && t <= asdu.F_SG_NA_1:
		return PermFileWrite
	}
	return 0
}

// RequiredPermissionOf is the permission request a needs: that of its
// type, except that F_SC_NA_1 with the qualifier "delete file" needs file
// management, the permission "to delete existing files on a device"
// (IEC TS 60870-5-7:2025 8.2, FILEMNGT).
func RequiredPermissionOf(a *asdu.ASDU) Permission {
	if a.Type == asdu.F_SC_NA_1 {
		for _, o := range a.Objects {
			if c, ok := o.(asdu.FileCall); ok && c.Qualifier&0x0F == asdu.FileDelete {
				return PermFileManagement
			}
		}
	}
	return RequiredPermission(a.Type)
}

// Authorized reports whether any of the roles may request type t.
func Authorized(roles []AccessRole, t asdu.TypeID) bool {
	return granted(roles, RequiredPermission(t))
}

// AuthorizedASDU reports whether any of the roles may make request a; see
// [RequiredPermissionOf].
func AuthorizedASDU(roles []AccessRole, a *asdu.ASDU) bool {
	return granted(roles, RequiredPermissionOf(a))
}

func granted(roles []AccessRole, p Permission) bool {
	if p == 0 {
		return true
	}
	for _, r := range roles {
		if r.Has(p) {
			return true
		}
	}
	return false
}

// IsMonitorData reports whether type t carries the runtime data values of
// the monitor direction that only a role with MONITOR DATA may receive,
// "whether sent spontaneously or sent in response to a request"
// (IEC TS 60870-5-7:2025 8.2): the process information types 1 to 44.
// End of initialization, confirmations of commands (which mirror the
// control-direction type), file transfer and security messages are not.
func IsMonitorData(t asdu.TypeID) bool { return t >= 1 && t <= 44 }
