using System;
using System.Collections.Generic;
using System.IO;
using System.Text.RegularExpressions;

public static class SelectionPolicy
{
    static readonly Regex Offensive = new Regex(@"^/Game/FX/Particle/PC/Male/(Sorcerer|Elementalist|Ranger|Fighter|Gladiator|Assassin)/", RegexOptions.IgnoreCase);
    static readonly Regex Warning = new Regex(@"heal|shield|barrier|protect|targetmark|abnormal|debuff|buff|telegraph|decal|ground|circle|(?<![a-z])range(?![a-z])|area|aura|marker|notify|indicator", RegexOptions.IgnoreCase);
    static readonly string[] Cosmetics = {
        "/Game/FX/Particle/Common/Item_Enchant/", "/Game/FX/Particle/Common/Wing/",
        "/Game/FX/Particle/Common/Halo/", "/Game/FX/Particle/VEH/",
        "/Game/FX/Particle/PC/Common/SocialAction/"
    };
    public static bool IsCandidate(string path)
    {
        if (string.IsNullOrEmpty(path) || path.Contains("\\") || path.Contains("..") || path.Contains(":")) return false;
        string name = path.Substring(path.LastIndexOf('/') + 1);
        if (!name.StartsWith("P_", StringComparison.OrdinalIgnoreCase) || name.EndsWith("_Mesh", StringComparison.OrdinalIgnoreCase)) return false;
        foreach (string prefix in Cosmetics)
            if (path.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
                return !Regex.IsMatch(name, "heal|shield|barrier|targetmark|abnormal|telegraph|decal|notify|indicator", RegexOptions.IgnoreCase);
        return Offensive.IsMatch(path) && !Warning.IsMatch(path);
    }
    public static bool HasGameplayReference(string path) => Warning.IsMatch(path);
    public static bool IsProtectedRoot(string path)
    {
        if (IsCandidate(path)) return false;
        if (Regex.IsMatch(path, @"/(Monster|NPC|Map|MapDataLayer|EnvObj|EnvFX|FO|Cinematic|Cleric|Chanter|Templar)/", RegexOptions.IgnoreCase)) return true;
        if (path.StartsWith("/Game/FX/Particle/Module/", StringComparison.OrdinalIgnoreCase)) return true;
        if (path.StartsWith("/Game/FX/Particle/PC/Common/", StringComparison.OrdinalIgnoreCase)) return true;
        if (path.StartsWith("/Game/FX/Act/Common/", StringComparison.OrdinalIgnoreCase)) return true;
        return Warning.IsMatch(path);
    }
    public static HashSet<string> ProtectedClosure(Dictionary<string, HashSet<string>> graph)
    {
        var visited = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        var queue = new Queue<string>();
        foreach (string path in graph.Keys) if (IsProtectedRoot(path) && visited.Add(path)) queue.Enqueue(path);
        while (queue.Count > 0)
        {
            string path = queue.Dequeue();
            if (!graph.TryGetValue(path, out var links)) continue;
            foreach (string target in links) if (visited.Add(target)) queue.Enqueue(target);
        }
        return visited;
    }
    public static string SourceFile(string root, string package)
    {
        if (!package.StartsWith("/Game/", StringComparison.Ordinal) || package.Contains("..") || package.Contains(':') || package.Contains('\\'))
            throw new InvalidDataException("Invalid package path");
        string content = Path.GetFullPath(Path.Combine(root, "AION2", "Content")) + Path.DirectorySeparatorChar;
        string path = Path.GetFullPath(Path.Combine(content, package.Substring(6).Replace('/', Path.DirectorySeparatorChar) + ".uasset"));
        if (!path.StartsWith(content, StringComparison.OrdinalIgnoreCase)) throw new InvalidDataException("Path leaves source root");
        return path;
    }
}
