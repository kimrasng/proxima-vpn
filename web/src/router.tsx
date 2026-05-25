import { createBrowserRouter, Navigate } from "react-router-dom";
import NotFound from "./pages/NotFound";
import AdminLayout from "./layouts/AdminLayout";
import UserLayout from "./layouts/UserLayout";
import AuthLayout from "./layouts/AuthLayout";
import AdminAuthGuard from "./components/AdminAuthGuard";
import UserAuthGuard from "./components/UserAuthGuard";

import Dashboard from "./pages/admin/Dashboard";
import Nodes from "./pages/admin/Nodes";
import NodeGroups from "./pages/admin/NodeGroups";
import Plans from "./pages/admin/Plans";
import Users from "./pages/admin/Users";
import PlanRequests from "./pages/admin/PlanRequests";
import AdminAnnouncements from "./pages/admin/Announcements";
import Settings from "./pages/admin/Settings";
import TwoFactor from "./pages/admin/TwoFactor";
import NodeInbounds from "./pages/admin/NodeInbounds";
import UserTemplates from "./pages/admin/UserTemplates";

import Login from "./pages/auth/Login";
import AdminLogin from "./pages/auth/AdminLogin";
import Register from "./pages/auth/Register";

import Devices from "./pages/user/Devices";
import Traffic from "./pages/user/Traffic";
import PlanInfo from "./pages/user/PlanInfo";
import AccountSettings from "./pages/user/AccountSettings";
import UserAnnouncements from "./pages/user/Announcements";

export const router = createBrowserRouter([
  {
    path: "/",
    element: <Navigate to="/portal/devices" replace />,
  },
  {
    element: <AuthLayout />,
    children: [
      { path: "/login", element: <Login /> },
      { path: "/register", element: <Register /> },
      { path: "/admin/login", element: <AdminLogin /> },
    ],
  },
  {
    element: (
      <AdminAuthGuard>
        <AdminLayout />
      </AdminAuthGuard>
    ),
    children: [
      { path: "/admin/dashboard", element: <Dashboard /> },
      { path: "/admin/nodes", element: <Nodes /> },
      { path: "/admin/nodes/:nodeId/inbounds", element: <NodeInbounds /> },
      { path: "/admin/node-groups", element: <NodeGroups /> },
      { path: "/admin/plans", element: <Plans /> },
      { path: "/admin/user-templates", element: <UserTemplates /> },
      { path: "/admin/users", element: <Users /> },
      { path: "/admin/plan-requests", element: <PlanRequests /> },
      { path: "/admin/announcements", element: <AdminAnnouncements /> },
      { path: "/admin/settings", element: <Settings /> },
      { path: "/admin/2fa", element: <TwoFactor /> },
    ],
  },
  {
    element: (
      <UserAuthGuard>
        <UserLayout />
      </UserAuthGuard>
    ),
    children: [
      { path: "/portal/devices", element: <Devices /> },
      { path: "/portal/traffic", element: <Traffic /> },
      { path: "/portal/plan", element: <PlanInfo /> },
      { path: "/portal/account", element: <AccountSettings /> },
      { path: "/portal/announcements", element: <UserAnnouncements /> },
    ],
  },
  {
    path: "*",
    element: <NotFound />,
  },
]);
